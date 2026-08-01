// The RFX_UI icon vocabulary (docs/RFX-UI.md). Authors name an icon in
// their manifest; this map owns the glyphs. The set is CLOSED — it must
// stay in lockstep with rfx.Icons in internal/rfx/rfx.go, which rejects
// unknown names at load, so an unmapped name can never reach the renderer.
import {
  Play, Zap, Plus, Check, X, Upload, Download, History, FileDiff,
  GitBranch, GitCommitHorizontal, FolderGit2, Terminal, Database, Globe,
  Wrench, Package, Bug, Shield, Sparkles, Rocket, RefreshCw, Search,
  Trash2, Eye, Flame
} from 'lucide-svelte';

export const RFX_ICONS = {
  'play': Play,
  'zap': Zap,
  'plus': Plus,
  'check': Check,
  'x': X,
  'upload': Upload,
  'download': Download,
  'history': History,
  'file-diff': FileDiff,
  'git-branch': GitBranch,
  'git-commit': GitCommitHorizontal,
  'folder-git': FolderGit2,
  'terminal': Terminal,
  'database': Database,
  'globe': Globe,
  'wrench': Wrench,
  'package': Package,
  'bug': Bug,
  'shield': Shield,
  'sparkles': Sparkles,
  'rocket': Rocket,
  'refresh': RefreshCw,
  'search': Search,
  'trash': Trash2,
  'eye': Eye,
  'flame': Flame
};

/** Resolve an icon name to its component, with a per-context fallback. */
export function rfxIcon(name, fallback = Play) {
  return RFX_ICONS[name] ?? fallback;
}
