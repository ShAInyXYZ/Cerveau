package api

import "cerveau/internal/rfx"

type PlannerBuildInfo struct {
	Name             string                     `json:"name"`
	Version          string                     `json:"version,omitempty"`
	Origin           string                     `json:"origin"`
	ContentSHA256    string                     `json:"content_sha256,omitempty"`
	Precedence       string                     `json:"precedence"`
	Loaded           bool                       `json:"loaded"`
	IgnoredInstalled []rfx.IgnoredInstalledPack `json:"ignored_installed"`
	Error            string                     `json:"error,omitempty"`
}

type BuildInfo struct {
	Version  string           `json:"version"`
	Revision string           `json:"revision"`
	Planner  PlannerBuildInfo `json:"planner"`
}

// BinaryBuildInfo reads only immutable build assets. It is safe before config
// loading and never claims a runtime loader has actually loaded this bundle.
func BinaryBuildInfo() BuildInfo {
	info := BuildInfo{Version: Version, Revision: BuildRevision, Planner: PlannerBuildInfo{
		Name: "planner", Origin: "builtin", Precedence: "builtin-wins", IgnoredInstalled: []rfx.IgnoredInstalledPack{},
	}}
	p, err := rfx.BuiltinPlanner()
	if err != nil {
		info.Planner.Error = err.Error()
		return info
	}
	info.Planner.Name = p.Pack
	info.Planner.Version = p.Version
	info.Planner.ContentSHA256 = p.ContentSHA256
	return info
}

func (a *API) buildInfo() BuildInfo {
	info := BinaryBuildInfo()
	if a.rfxLoader == nil || info.Planner.Error != "" {
		return info
	}
	for _, p := range a.rfxLoader.Packs() {
		if p.Pack == info.Planner.Name && p.Origin == "builtin" && p.ContentSHA256 == info.Planner.ContentSHA256 && p.Version == info.Planner.Version && p.Panel != "" {
			if _, err := p.ReadPanel(); err != nil {
				info.Planner.Error = err.Error()
				return info
			}
			info.Planner.Loaded = true
			info.Planner.IgnoredInstalled = p.IgnoredInstalled
			break
		}
	}
	return info
}
