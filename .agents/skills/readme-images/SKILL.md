---
name: readme-images
description: Add, replace or preview images in the slop-police README. Use when the user hands over a screenshot, logo or other picture for the README, or wants to see how the README renders.
---

# README images

## Adding or replacing an image

1. Convert to WebP into `assets/`: `cwebp -q 90 -quiet input.png -o assets/name.webp`. Never commit PNG or JPEG.
2. Ship a dark and a light variant (`name-dark.webp`, `name-light.webp`) at the same pixel size; pad the narrower one with its background colour (`magick in.png -background white -gravity center -extent WxH -quality 90 out.webp`) so both render at the same size.
3. Reference them with `<picture>`, dark as the `<source>`, light as the `<img>` fallback, with alt text that carries any words in the image:

   ```html
   <picture>
     <source media="(prefers-color-scheme: dark)" srcset="assets/name-dark.webp?v=1">
     <img src="assets/name-light.webp?v=1" alt="...">
   </picture>
   ```

4. GitHub's image proxy caches by URL. When replacing an existing image under the same file name, bump `?v=` on both variants.

## Previewing

```sh
gh extension install yusukebe/gh-markdown-preview
gh markdown-preview README.md
```

It serves the README at `http://localhost:3333` with GitHub styling and local images; switch the OS theme to check both variants.
