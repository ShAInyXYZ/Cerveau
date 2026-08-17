package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"cerveau/internal/api"
	"cerveau/internal/codeintel"
	"cerveau/internal/config"
	"cerveau/internal/guard"
	"cerveau/internal/llm"
	"cerveau/internal/loop"
	"cerveau/internal/memory"
	"cerveau/internal/rfx"
	"cerveau/internal/server"
	"cerveau/internal/session"
	"cerveau/internal/skills"
	"cerveau/internal/tools"
	"cerveau/internal/window"
)

func main() {
	configPath := flag.String("config", config.DefaultPath(), "path to config.json")
	flag.Parse()

	cfg, err := config.LoadOrCreate(*configPath)
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	sess, err := session.NewFSStore(cfg.SessionsDir)
	if err != nil {
		slog.Error("session store", "err", err)
		os.Exit(1)
	}
	sess.SetWorkspace(cfg.Workspace)

	tsEp, err := memory.EnsureTypesense(cfg, *configPath)
	var indexer *memory.Indexer
	var tsClient *memory.TSClient
	var curator *memory.Curator
	if err != nil {
		slog.Warn("typesense unavailable — running at T0 (no memory recall)", "err", err)
	} else {
		if tsEp.Managed {
			slog.Info("typesense: managed sidecar", "url", tsEp.URL)
		}
		home, _ := os.UserHomeDir()
		tsClient = memory.NewTSClient(tsEp.URL, tsEp.Key)
		embedURL := ""
		if memory.EndpointHealthy(cfg.Endpoints.Embedder) {
			embedURL = cfg.Endpoints.Embedder
			slog.Info("embedder online — hybrid vector recall", "url", embedURL)
		}
		indexer = memory.NewIndexer(
			tsClient,
			cfg.SessionsDir,
			filepath.Join(home, ".crv", "index-cursor.json"),
			embedURL,
		)
		indexer.Start(context.Background())
		// one-time repair: stamp session_id on older semantic docs from their sources
		go func() {
			time.Sleep(3 * time.Second) // let schema/index settle first
			n, err := tsClient.BackfillSessionIDs(context.Background())
			if err != nil {
				slog.Warn("session_id backfill failed", "err", err)
			} else {
				slog.Info("session_id backfill", "repaired", n)
			}
		}()
		curator = memory.NewCurator(tsClient, filepath.Join(home, ".crv", "pending-semantic.jsonl"),
			func() bool { return memory.EndpointHealthy(cfg.Endpoints.Typesense) })
		go func() {
			t := time.NewTicker(10 * time.Second)
			defer t.Stop()
			for range t.C {
				curator.DrainPending(context.Background())
			}
		}()
	}

	// drainBackground is set once the loop exists; the signal handler calls it
	// to let in-flight turn-boundary work (distill + semantic promotion) finish
	// before we tear down Typesense.
	var drainBackground func()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		if drainBackground != nil {
			drainBackground() // brief grace for async promotion
		}
		if indexer != nil {
			indexer.Stop()
		}
		if tsEp != nil {
			tsEp.Close()
		}
		os.Exit(0)
	}()

	a := api.New(cfg, sess)
	a.SetConfigPath(*configPath)
	if tsClient != nil {
		a.SetMemory(tsClient)
	}

	llmClient := llm.NewClient(cfg.Endpoints.Model)
	sctx := &tools.SessionContext{}

	codeStore, err := codeintel.OpenStore(codeintel.DBPathFor(cfg.Workspace))
	if err != nil {
		slog.Warn("code graph unavailable", "err", err)
	}
	var ci *codeintel.Indexer
	if codeStore != nil {
		ci = codeintel.NewIndexer(codeStore, cfg.Workspace)
		a.SetCodeIntel(ci)
		go func() {
			rep, err := ci.Index(context.Background())
			if err != nil {
				slog.Warn("code index", "err", err)
			} else {
				slog.Info("code graph indexed", "parsed", rep.Parsed, "skipped", rep.Skipped, "removed", rep.Removed)
			}
		}()
	}

	apPatch := tools.NewApplyPatch()
	buildEntries := func(ws string, store *codeintel.Store) []tools.Entry {
		entries := []tools.Entry{
			{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe, IngressCap: 8000, RetryClass: "args"},
			{Tool: tools.NewGrep(ws), RiskTier: tools.RiskSafe, IngressCap: 8000, RetryClass: "args"},
			{Tool: tools.NewGlob(ws), RiskTier: tools.RiskSafe, IngressCap: 4000, RetryClass: "args"},
			{Tool: tools.NewEdit(ws), RiskTier: tools.RiskSensitive, Modes: []string{tools.ModeDiscussion, tools.ModeAutopilot, tools.ModeBrainstorming}, IngressCap: 2000, RetryClass: "args"},
			{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSensitive, Modes: []string{tools.ModeDiscussion, tools.ModeAutopilot, tools.ModeBrainstorming}, IngressCap: 2000, RetryClass: "args"},
			{Tool: apPatch, RiskTier: tools.RiskSensitive, Modes: []string{tools.ModeDiscussion, tools.ModeAutopilot, tools.ModeBrainstorming}, IngressCap: 3000, RetryClass: "args"},
			{Tool: tools.NewBash(ws), RiskTier: tools.RiskDangerous, Modes: []string{tools.ModeAutopilot}, IngressCap: 8000, RetryClass: "transient"},
			// serve: long-lived static server for the workspace — the one thing
			// bash cannot do (it kills its process group). Safe: static files only.
			{Tool: tools.NewServe(ws), RiskTier: tools.RiskSafe, Modes: []string{tools.ModeAutopilot}, IngressCap: 1000, RetryClass: "args"},
			// check_page: headless-browser feedback — console errors + rendered-DOM
			// checks. Without it, "the page doesn't render" is undebuggable from
			// static reads alone.
			{Tool: tools.NewCheckPage(ws), RiskTier: tools.RiskSafe, IngressCap: 3000, RetryClass: "args"},
			{Tool: tools.NewCommitPlan(a.Writer, sctx), RiskTier: tools.RiskSafe, Modes: []string{tools.ModeDiscussion}, IngressCap: 2000, RetryClass: "args"},
			{Tool: tools.NewAskUser(a.QuestionBroker(), sctx), RiskTier: tools.RiskSafe, IngressCap: 1000, RetryClass: "args"},
			{Tool: tools.NewWebFetch(), RiskTier: tools.RiskSafe, Modes: []string{tools.ModeBrainstorming}, IngressCap: 8000, RetryClass: "transient"},
		}
		if store != nil {
			entries = append(entries,
				tools.Entry{Tool: tools.NewFileMap(store), RiskTier: tools.RiskSafe, IngressCap: 6000, RetryClass: "args"},
				tools.Entry{Tool: tools.NewFindSymbol(store), RiskTier: tools.RiskSafe, IngressCap: 2000, RetryClass: "args"},
				tools.Entry{Tool: tools.NewFindReferences(store), RiskTier: tools.RiskSafe, IngressCap: 3000, RetryClass: "args"},
				tools.Entry{Tool: tools.NewOutlineFile(store), RiskTier: tools.RiskSafe, IngressCap: 4000, RetryClass: "args"},
			)
		}
		if curator != nil {
			entries = append(entries, tools.Entry{Tool: tools.NewRemember(curator, sctx), RiskTier: tools.RiskSafe, IngressCap: 1000, RetryClass: "args"})
		}
		return entries
	}

	registry := tools.NewRegistry(buildEntries(cfg.Workspace, codeStore)...)
	registry.SetWorkspace(cfg.Workspace)
	apPatch.SetRegistry(registry)
	grd := guard.New(cfg.Workspace)
	registry.SetGuard(grd.Check)
	registry.SetRemediator(func(tool string, args json.RawMessage) (json.RawMessage, error) {
		return grd.Remediate(tool, args, time.Now())
	})
	if ci != nil {
		registry.SetPostExec(func(name string, args json.RawMessage) {
			if name == "edit" || name == "write" {
				var a struct {
					Path string `json:"path"`
				}
				if json.Unmarshal(args, &a) == nil && a.Path != "" {
					ci.ReindexOnEdit(context.Background(), a.Path)
				}
			}
		})
	}
	a.SetSessionContext(sctx)
	winMgr := window.NewManager(cfg.ModelCtx, 2048, window.NewHTTPCounter(cfg.Endpoints.Model))
	agentLoop := loop.New(llmClient, registry, a.Writer, sess.EventsPath, winMgr)

	// RFX: the loader validates step tools against the LIVE registry — which
	// is replaced on workspace switch, so the predicate follows a pointer.
	currentReg := &registry
	rfxLoader := rfx.NewLoader(filepath.Join(cfg.SessionsDir, "..", "rfx"), func(name string) bool {
		if currentReg == nil || *currentReg == nil {
			return false
		}
		_, ok := (*currentReg).Entry(name)
		return ok
	})
	for _, le := range rfxLoader.Errors() {
		slog.Warn("rfx: manifest rejected", "file", le.Path, "err", le.Err)
	}
	if n := len(rfxLoader.List()); n > 0 {
		slog.Info("rfx: reflexes loaded", "count", n)
	}
	agentLoop.SetReflexes(rfxLoader)
	a.SetRfxLoader(rfxLoader)
	// Per-session workspace: a session (esp. an instant scratch session) carries
	// its OWN workspace in meta; fall back to the live global cfg.Workspace.
	agentLoop.SetWorkspaceFunc(func(sessionID string) string {
		if m, err := sess.Get(sessionID); err == nil && m.Workspace != "" {
			return m.Workspace
		}
		return cfg.Workspace
	})
	// instant sessions never promote to long-term semantic memory
	agentLoop.SetInstantCheck(sess.IsInstant)
	// tell the model what the harness itself is running, so it never suggests
	// serving user work on ports the stack already occupies (e.g. :8080 = LLM)
	agentLoop.SetStackFunc(func() string {
		return fmt.Sprintf(
			"Local stack ALREADY RUNNING on this machine (never serve anything on these ports): "+
				"Cerveau API on localhost%s, LLM server (llama.cpp) on %s, embedder on %s, Typesense on %s. "+
				"When you need to serve something you build, pick a free port such as 8000, 3000, or 8888 — and check it's free first.",
			cfg.Addr, cfg.Endpoints.Model, cfg.Endpoints.Embedder, cfg.Endpoints.Typesense)
	})
	// TTL sweeper: delete instant (scratch) sessions idle > 24h. Runs at boot and
	// every 30 min thereafter.
	const instantTTL = 24 * time.Hour
	go func() {
		if n := sess.SweepInstant(instantTTL); len(n) > 0 {
			slog.Info("swept expired instant sessions", "count", len(n))
		}
		t := time.NewTicker(30 * time.Minute)
		defer t.Stop()
		for range t.C {
			if n := sess.SweepInstant(instantTTL); len(n) > 0 {
				slog.Info("swept expired instant sessions", "count", len(n))
			}
		}
	}()
	if tsClient != nil {
		agentLoop.SetRecall(memory.NewRecall(tsClient, cfg.SessionsDir, memory.EndpointHealthy(cfg.Endpoints.Embedder)))
	}
	if curator != nil {
		agentLoop.SetCurator(curator)
	}
	skillLoader := skills.NewLoader(filepath.Join(cfg.SessionsDir, "..", "skills"))
	agentLoop.SetSkills(skillLoader, func(defs []skills.SkillTool) []tools.Tool {
		return tools.SkillTools(defs, cfg.Workspace, grd.Check)
	})
	a.SetSkillLoader(skillLoader)
	a.SetLoop(agentLoop)
	drainBackground = func() { agentLoop.WaitBackground(3 * time.Second) }

	a.SetWorkspaceChanger(func(ws string) error {
		abs, err := filepath.Abs(ws)
		if err != nil {
			return err
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("not a directory: %s", ws)
		}
		newStore, err := codeintel.OpenStore(codeintel.DBPathFor(abs))
		if err != nil {
			return fmt.Errorf("code graph: %w", err)
		}
		newCi := codeintel.NewIndexer(newStore, abs)
		newGrd := guard.New(abs)
		newReg := tools.NewRegistry(buildEntries(abs, newStore)...)
		newReg.SetWorkspace(abs)
		apPatch.SetRegistry(newReg)
		newReg.SetGuard(newGrd.Check)
		newReg.SetRemediator(func(tool string, args json.RawMessage) (json.RawMessage, error) {
			return newGrd.Remediate(tool, args, time.Now())
		})
		newReg.SetPostExec(func(name string, args json.RawMessage) {
			if name == "edit" || name == "write" {
				var p struct {
					Path string `json:"path"`
				}
				if json.Unmarshal(args, &p) == nil && p.Path != "" {
					newCi.ReindexOnEdit(context.Background(), p.Path)
				}
			}
		})
		cfg.Workspace = abs
		sess.SetWorkspace(abs)
		if err := config.Save(*configPath, cfg); err != nil {
			return err
		}
		*currentReg = newReg // rfx step-tool validation follows the new registry
		agentLoop.SetRegistry(newReg)
		agentLoop.SetSkills(skillLoader, func(defs []skills.SkillTool) []tools.Tool {
			return tools.SkillTools(defs, abs, newGrd.Check)
		})
		a.SetCodeIntel(newCi)
		go func() {
			rep, err := newCi.Index(context.Background())
			if err != nil {
				slog.Warn("code index (new workspace)", "err", err)
			} else {
				slog.Info("workspace changed + indexed", "workspace", abs, "parsed", rep.Parsed)
			}
		}()
		slog.Info("workspace changed", "workspace", abs)
		return nil
	})

	srv := server.New(cfg.Addr, a)

	// Remote access: the phone app pairs with a short ID printed here (the
	// machine's console is proof of physical access), and the core refuses a
	// non-localhost bind until a token exists.
	if cfg.RemoteAccessToken == "" {
		pairID, err := server.EnsurePairID()
		if err != nil {
			slog.Error("pair id", "err", err)
			os.Exit(1)
		}
		if isLocalBind(cfg.Addr) {
			slog.Info("pairing id (for the phone app)", "pair_id", pairID)
			fmt.Printf("\n  ◈ cerveau pairing id: %s  — enter it in the phone app to pair\n\n", pairID)
		} else {
			slog.Error("refusing non-localhost bind without remote_access_token in config — pair a phone first (run on 127.0.0.1)", "addr", cfg.Addr)
			os.Exit(1)
		}
	}

	slog.Info("cerveau online", "addr", cfg.Addr, "sessions", cfg.SessionsDir)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

func isLocalBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return host == "" || host == "127.0.0.1" || host == "localhost" || host == "::1"
}
