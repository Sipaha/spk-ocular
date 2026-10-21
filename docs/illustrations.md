# Interface illustrations

## Disconnected workspace

Two illustrations decorate unopened or closed connections; the asset inventory
also retains the lighthouse reference variant:

- `web/src/assets/disconnected-lighthouse-drawn.webp`: drawn lighthouse for the
  navigation panel.
- `web/src/assets/disconnected-lighthouse.webp`: retained lighthouse reference
  variant; it is not used by the current workspace CSS.
- `web/src/assets/disconnected-mountains.webp`: schematic mountains and lake for
  the main connection-status panel, also used before selecting a connection.

Each illustration is anchored to its own panel. Their transparent backgrounds
retain the normal panel base colors; they do not form a shared panorama or appear
behind connected resource tables. The application makes no external image
request. The drawings are decorative and contain no labels or interactive controls.
Resource-section and connection glyphs remain native SVGs.

The lighthouse is a fixed 512 × 342 px lower-left decoration, offset 55 px
past the left edge. Panel resizing clips it rather than scaling it. Its soft
fading beam belongs to the same illustration; there is no separate bright cone.
The body and beam retain the selected drawing's relative lighting at 62% overall
opacity. The main mountains remain independent of this sidebar artwork.

The assets were generated with the built-in OpenAI image generation tool and
encoded as lossless WebP with alpha preserved. The drawn lighthouse source is
1536 × 1024; the mountains source is 1672 × 941. The owner-provided lighthouse
reference guides its outlined silhouette, angular rocks and restrained light;
reference UI and text are not included.

### Lighthouse prompt

> Use case: stylized-concept. Asset type: transparent decorative raster illustration for the lower-left corner of a dark desktop application's navigation sidebar.
> Input image: the user's attached image is a STYLE AND SUBJECT REFERENCE ONLY. Generate a new standalone illustration, not a screenshot, with no UI, lettering, border, or background panel.
> Primary request: a hand-drawn nautical lighthouse closely matching the reference's restrained illustrated quality: a dark slender slightly tapered lighthouse with pale slate edge outlines, an elegant small rounded lantern roof, two softly warm ivory windows, standing on large angular dark navy rocks. A couple of low simplified clouds near the rocks. Quiet graphic illustration with subtle brush-painted texture and a small number of faceted rock shapes; not photorealistic, not 3D.
> Composition: wide landscape canvas approximately1536x1024. Lighthouse lamp center around20percent from left and55percent from top, tower extending down to the rocky ledge around88percent from top. Rock base along bottom quarter, wider than tower. Keep upper45percent and most of upper-right completely transparent, with ample empty area for existing navigation labels. A single soft triangular light beam points from the lantern to far right, very subdued warm gray, fading gently to transparent toward the right edge. Light beam must be at least4times wider than the tower and must not have a sharp opaque ending. The lighthouse and rocks are the focus, not the beam.
> Palette: low-contrast graphite, desaturated midnight navy/slate and muted blue-gray (#131b26,#1b2635,#283547), with fine pale gray-blue edge accents. Tiny warm window light only; beam is much dimmer than windows and blends into a #191e26 app surface.
> Transparency: true transparent alpha outside the tower, rocks, clouds and faint beam; no opaque sky, no dark rectangular backdrop, no checkerboard. Do not render reference text or menu icons. No slogans, no letters, no watermark, no scene extending into another panel. Preserve the reference's drawn outlined lighthouse silhouette and dark angular rocks.
>

### Mountains prompt

> Use case: stylized-concept. Asset type: self-contained schematic landscape decoration with true transparent alpha for the BOTTOM RIGHT of a dark infrastructure desktop app main panel. A NEW illustration, not a UI mockup and not a photograph. Wide landscape composition roughly 1920x1080. Minimalist FLAT VECTOR-LIKE low-poly illustration: a few layered angular mountain silhouettes at the far right, simple triangular pine trees and a small curved lake/river in the lower foreground. Large clean faceted polygon shapes with limited flat navy/slate colors (#111923, #1b2635, #263348, #34455d), clear graphic silhouette. Like a restrained illustrated game menu, NOT realistic terrain, NOT photoreal, NOT a detailed matte painting. No texture or photographic surface detail, no fog effects, no stars or dramatic lighting. Mountains occupy only the bottom-right quarter, lake and shoreline lie along the bottom 15 percent, lightly curved horizontal layers. The TOP 65 percent and LEFT 45 percent must be empty genuinely TRANSPARENT with no background rectangle, so UI status and a Connect button can sit in open space above/left. Outside the mountain/lake/tree forms use true transparent alpha, no black fill and no checkerboard. No lighthouse: the sidebar has a different independent image. Keep the scene low contrast and quiet on a #111820 surface. No text, no quotes, no icons, no UI controls, no frame, no logo, no watermark. The most important requirement is a schematic, flat polygon illustration closely matching the graphic simplicity of the user's reference, never realism.
