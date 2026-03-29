# wez-cc-viewer

A TUI dashboard for monitoring [Claude Code](https://docs.anthropic.com/en/docs/claude-code) instances running across [WezTerm](https://wezfurlong.org/wezterm/) workspaces.

![demo](https://raw.githubusercontent.com/sorafujitani/wez-cc-viewer/main/assets/demo.png)

## Features

- Detects all Claude Code instances across WezTerm workspaces
- Shows real-time status: **active** (working) / **idle** (waiting for input)
- Displays task title, working directory, and pane ID for selected agent
- Switch to any agent's workspace with Enter
- Auto-refreshes every 3 seconds
- Keyboard navigation (j/k, arrows, g/G)

## How it works

```
┌─────────────────────────────────┐
│  wez-cc-viewer (Go binary)      │
│  - wezterm cli list (panes)     │
│  - ps (process tree)            │
│  - Bubbletea TUI                │
│  - SetUserVar escape sequence   │
└──────────┬──────────────────────┘
           │ \033]1337;SetUserVar=...
┌──────────▼──────────────────────┐
│  WezTerm Lua config             │
│  - user-var-changed event       │  ← 3 lines to add
│  - SwitchToWorkspace action     │
└─────────────────────────────────┘
```

**Agent detection**: Cross-references `wezterm cli list` (pane/TTY info) with `ps -eo pid,ppid,tty,comm` (process tree). Walks the ppid chain from each pane's foreground process to find a `claude` ancestor.

**Running vs idle**: Claude Code spawns a `caffeinate` child process while actively working. If a `caffeinate` child exists under the Claude process, it's "running"; otherwise "idle".

**Workspace switching**: Sends an [iTerm2 SetUserVar](https://iterm2.com/documentation-escape-codes.html) escape sequence (`\033]1337;SetUserVar=switch_workspace=<base64>\007`). WezTerm fires its `user-var-changed` Lua event, which your config handles with `SwitchToWorkspace`.

## Installation

### Go install

```sh
go install github.com/sorafujitani/wez-cc-viewer@latest
```

### Build from source

```sh
git clone https://github.com/sorafujitani/wez-cc-viewer.git
cd wez-cc-viewer
go build -o wez-cc-viewer
```

## Setup

Add the following to your `wezterm.lua`:

```lua
local wezterm = require("wezterm")
local act = wezterm.action

-- 1. Handle workspace switch from wez-cc-viewer
wezterm.on("user-var-changed", function(window, pane, name, value)
  if name == "switch_workspace" then
    window:perform_action(act.SwitchToWorkspace({ name = value }), pane)
  end
end)

-- 2. Resolve binary path (cached after first call)
--    Needed because WezTerm panes may not inherit your shell's full PATH
--    (e.g. when using mise, asdf, or Go installed via Homebrew).
local _bin_cache = nil
local function find_wez_cc_viewer()
  if _bin_cache then return _bin_cache end
  local ok, stdout = wezterm.run_child_process({
    os.getenv("SHELL") or "/bin/zsh", "-lic", "which wez-cc-viewer",
  })
  if ok and stdout then
    local path = stdout:gsub("%s+$", "")
    if path ~= "" then
      _bin_cache = path
      return path
    end
  end
  return nil
end

-- 3. Add a keybinding to launch the dashboard
config.keys = {
  {
    key = "a",
    mods = "LEADER",
    action = wezterm.action_callback(function(window, pane)
      local bin = find_wez_cc_viewer()
      if not bin then
        window:toast_notification("wezterm", "wez-cc-viewer not found in PATH", nil, 3000)
        return
      end
      local new_pane = pane:split({
        direction = "Bottom",
        args = { bin },
      })
      window:perform_action(act.TogglePaneZoomState, new_pane)
    end),
  },
}
```

> **Note**: If `wez-cc-viewer` is in a standard location (e.g. `/usr/local/bin`), you can skip `find_wez_cc_viewer` and use `args = { "wez-cc-viewer" }` directly.

## Keybindings

| Key | Action |
|-----|--------|
| `j` / `↓` | Move selection down |
| `k` / `↑` | Move selection up |
| `Enter` | Switch to selected agent's workspace |
| `r` | Manual refresh |
| `g` / `G` | Jump to first / last |
| `q` / `Esc` | Quit |

## Requirements

- [WezTerm](https://wezfurlong.org/wezterm/) (with `wezterm` CLI in PATH)
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) running in one or more WezTerm panes
- macOS (uses `ps` and `/dev/fd/0` for TTY detection)

## License

MIT
