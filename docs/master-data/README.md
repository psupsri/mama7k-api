# Master data — CSVs

Import in Admin → นำเข้าไฟล์: pick the entity, upload the CSV, check the preview, then confirm.
Any order: `characters`, `pets`, `sets`, `rings`, `stats`.

| File | Rows | Notes |
| --- | --- | --- |
| `characters.csv` | 116 | |
| `pets.csv` | 14 | |
| `sets.csv` | 9 | |
| `rings.csv` | 32 | Rows with an empty `name_th` fail the import. |
| `stats.csv` | 11 | The stats used in build recommendations (shown as "คริ 100+"). No images. |

## Columns

Every file has only `id`, `name_th`, `name_en`, `image`, `active`. Skills are not master data: every character has
an upper and a lower skill, and builds refer to them by character and position. The site
recommends teams; it does not describe what items do, so there are no effect, rarity or unit columns.


- `id` — a running number per type (1, 2, 3 …). It is what builds and lineups point to, and a re-import
  matches rows by it, so never renumber or reuse an id once imported. New rows take the next number; the admin form
  assigns it automatically.
- `name_th` — the Thai community name; this is what users see. Required.
- `name_en` — English name, optional; search matches it too.
- `active` — `true`/`false`; empty keeps the current value on update. Rows not in a file are left unchanged — to retire
  an item set `active` to `false` (items are never deleted).

## Images

The `image` column holds a path relative to `MEDIA_PUBLIC_URL` (set in `api/.env`):

```
master/characters/001_ludi.webp
master/pets/01_lu.webp
master/equipment-sets/01_attack.webp
master/accessories/ring_01.webp
```

The API shows `MEDIA_PUBLIC_URL + "/" + image`, so each path is the object key in the R2 bucket: upload
`characters/001_ludi.webp` to key `master/characters/001_ludi.webp` (the `rings` folder goes to `master/accessories/`).
`MEDIA_PUBLIC_URL` is the bucket's public URL (Settings → Public access, `https://pub-….r2.dev`), not the S3 API endpoint.

An empty `image` cell keeps the current image; a path is checked for format only (`a-z A-Z 0-9 _ -`, `/` between parts,
`.webp/.png/.jpg`), not for whether the file exists.
