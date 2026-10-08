# Website and intro assets

The public website is the static `site/` directory. Its timed HTML introduction
also supplies the animated images in both READMEs, so changes to the intro must
update the GIFs in the same change.

## Entrypoints and flow

- `site/index.html` and `site/index.zh.html`: English and Chinese homepages.
- `site/style.css`: shared website styles, including the intro frame and community.
- `site/intro.html`: one self-contained HTML animation with nine timed scenes.
- `assets/san-intro.gif` and `assets/san-intro-dark.gif`: light and dark README exports.
- `tools/site/export-intro.mjs`: Chrome capture and FFmpeg GIF export.

The website uses the paper-colored intro by default. `?variant=readme` uses a
plain GitHub-colored canvas and follows the browser's preferred color scheme.
`?theme=light` or `?theme=dark` overrides that preference. Both READMEs use a
`picture` element to select the appropriate GIF and link to the README variant
of the HTML intro.

There are no Go package dependencies in this asset pipeline. The site uses the
same teal for branding and a deeper teal for small text. Comic typography,
stickers and foreground motion remain; the intro has no background dot pattern
or grain. Community access uses text and links rather than QR images.

## Local preview

```bash
python3 -m http.server 8000 --bind 127.0.0.1 --directory site
```

Open `http://127.0.0.1:8000/` or `index.zh.html`. On the intro, click to pause,
use the Pause/Replay buttons, or press Space to pause and R to restart. Append
`?t=33` to freeze a scene for a screenshot; frozen mode disables CSS animation
and transitions so frame capture is deterministic.

## Regenerate the README GIFs

Install Node.js 24+, Google Chrome and FFmpeg, then run from the repository root:

```bash
node tools/site/export-intro.mjs
```

On macOS the tool uses the normal Google Chrome application path. On other
systems it uses `google-chrome`. Override that path when needed:

```bash
SAN_CHROME=/path/to/chromium node tools/site/export-intro.mjs
```

The exporter starts a temporary localhost server, waits for the comic font,
then uses `window.sanIntro.duration` and `window.sanIntro.seek(seconds)` to
capture the same 1280×720 timeline at 12 fps for both themes. It generates both
GIFs before replacing the checked-in assets and removes temporary frames.

## Validation and pitfalls

After changes, inspect the opening, a feature scene, session groups and the
closing scene for both variants and both README themes. Check pause/replay,
homepage demo tabs, and narrow-screen layout. Confirm the READMEs and homepages
have no QR image references, and run `git diff --check`.

Google Fonts requires network access for capture. The exporter fails if the
comic display font does not load; fix connectivity before regenerating instead
of checking in a fallback-font animation. Keep screenshot times on the HTML
timeline: elapsed browser time is affected by playback and scene pacing.

`.github/workflows/pages.yml` deploys `site/` when a site change reaches `main`.
Local preview and GIF generation do not publish the website.
