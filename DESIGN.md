# Design System — brz

## Product Context
- **What this is:** YAML-driven browser automation CLI for developers and LLM agents
- **Who it's for:** Developers, AI/LLM agent builders, DevOps engineers
- **Space/industry:** Developer tools, browser automation, agentic infrastructure
- **Project type:** Marketing site (GitHub Pages static site)

## Aesthetic Direction
- **Direction:** Retro-Futuristic / Industrial with early 2000s nostalgia
- **Decoration level:** Expressive (perspective grid, scanlines, CRT vignette, pixel ornaments, EQ bars)
- **Mood:** PS1 boot screen meets Wipeout meets a drum and bass record label. Terminal-native but elevated. Pixel-perfect edges, neon glow, hard digital energy.
- **Reference sites:** Linear.app (structure), Warp.dev (dark developer aesthetic), The Designers Republic (pixel/Wipeout art direction)

## Typography
- **Display/Pixel:** Press Start 2P (pixel font for logo, section labels, step headings, exit codes)
- **Body:** DM Sans (clean geometric sans for body text and headings)
- **Code/CLI:** Space Mono (monospaced for all terminal blocks, agent code, YAML)
- **Loading:** Google Fonts CDN
- **Scale:** 68px hero, 40px h2, 22px h3, 16px body, 14px mono body, 9px pixel labels

## Color
- **Approach:** Expressive (full neon palette, glow effects, text-shadow)
- **Background:** #06060e (deep navy-black)
- **Surface:** #0c0c18 (code blocks, cards)
- **Surface-alt:** #10101f (hover states)
- **Border:** #1a1a30 (2px solid, hard edges)
- **Primary text:** #d4d4e8 (cool off-white)
- **Bright text:** #eeeefc (headings)
- **Muted:** #5e5e80 (comments, descriptions)
- **Cyan accent:** #00ffee (primary brand, links, highlights, glow)
- **Magenta accent:** #ff0080 (secondary, ticker, section labels, hover)
- **Lime:** #39ff14 (terminal prompts, success)
- **Yellow:** #ffe600 (strings, values)
- **Pink:** #ff6ec7 (numbers)

## Spacing
- **Base unit:** 4px
- **Density:** Comfortable with generous section padding
- **Section spacing:** 100px between sections
- **Card gaps:** 2px (pixel-grid feel, border color shows through)

## Layout
- **Approach:** Grid-disciplined with pixel-perfect edges
- **Max content width:** 960px
- **Border radius:** 0 (hard pixel edges everywhere)
- **Grid gaps:** 2px solid (border bleeds through as grid lines)

## Motion
- **Approach:** Expressive but digital
- **PS1 boot sequence:** Stepped animation (steps(4)) for text reveal
- **Loading bar:** steps(20) for quantized/digital feel
- **Chromatic aberration:** Slow 4s text-shadow cycle on hero
- **EQ bars:** Stepped (steps(8)) pulsing at varied speeds
- **Ticker:** Linear infinite scroll, pixel font
- **Glitch:** Logo hover effect (steps(2))
- **Gradient slide:** Animated cyan-magenta top-edge on code blocks
- **Scanlines:** Fixed overlay, 4px repeat
- **CRT vignette:** Radial gradient darkening corners
- **Scroll:** Simple fade-up on intersection

## Decorative Elements
- Perspective grid canvas (Wipeout vanishing point)
- CRT scanline overlay
- CRT vignette (dark corners)
- Pixel corner ornaments (cyan/magenta L-brackets)
- Scrolling magenta ticker
- EQ visualizer bars
- ASCII art footer logo
- Animated gradient code block top-edge
- Blinking cursor in terminal

## Decisions Log
| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-04-15 | Initial design system | Retro-futuristic pixel aesthetic, PS1/Wipeout/dnb inspired |
| 2026-04-15 | Press Start 2P for pixel elements | Authentic 8-bit feel for labels and accents |
| 2026-04-15 | Zero border-radius | Hard pixel edges reinforce the retro-digital aesthetic |
| 2026-04-15 | 2px borders instead of 1px | Heavier weight matches pixel aesthetic, more visible grid structure |
| 2026-04-15 | EQ bars decoration | Evokes dnb/electronic music energy without audio |
