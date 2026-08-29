# Tablero UI

This directory contains the design system and frontend assets for `tablero web` — the embedded dashboard UI (see [#3](https://github.com/Gabriel100201/tablero/issues/3)).

## Structure

```
ui/
├── design-system/
│   ├── index.html          # Living styleguide — open in browser to preview all components
│   ├── event-horizon.css   # Design tokens + all component styles
│   └── screenshots/        # Reference screenshots from the design session
└── README.md               # This file
```

## Design system — Event Horizon

**Event Horizon** is the design language for Tablero. It is a dark-first, space-inspired system built around focus, gravity, and clarity.

| Token | Value | Role |
|---|---|---|
| `--void` | `#0A0A0D` | Page background |
| `--deep-space` | `#0F1116` | App shell background |
| `--surface` | `#14171D` | Card / panel background |
| `--elevated` | `#1B1F28` | Hover states, nested surfaces |
| `--accent` | `#FFB84D` | Primary CTA, active states, focus rings |
| Font | General Sans | Closest free alternative to Satoshi |

### Previewing the styleguide

```bash
# Any static file server works — for example:
npx serve ui/design-system
# then open http://localhost:3000
```

Or just open `ui/design-system/index.html` directly in a browser.

## Status

The design system is complete as a static prototype. The next step is implementing `tablero web` (issue [#3](https://github.com/Gabriel100201/tablero/issues/3)) using these tokens and components as the foundation.
