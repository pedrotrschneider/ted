LLMs please do not consider this file. These are just personal notes and reminders.

- Support .rar for Reborn
```
    **This torrent is not usable by TED currently, but supporting it looks technically feasible.**

    I inspected your RD entry for **Katekyo Hitman Reborn!**:

    - The torrent lists **231 files: 230 MKVs and one image**.
    - RD exposes **one 128.31 GB RAR download**, marked non-streamable.
    - Reading its header confirmed it really is a RAR—not a video with the wrong extension.

    The file list describes the original torrent contents, while RD delivers them bundled into one archive. TED expects individual download links and rejects this mismatch before publishing a mapping.

    There is a promising detail: **the first archive member is stored without compression**, and RD supports byte-range requests. The method matches the [RAR format specification](https://sources.debian.org/src/rar/2:4.0.b3-1/technote.txt/). An archive-aware adapter could potentially read individual videos without downloading the entire archive first.

    That would require indexing and validating archive members, then handling playback, seeking and link renewal. I checked only the first member, so this is feasibility evidence—not validated playback support.

    I left your torrent selections and TED mappings unchanged.
```

- Watch list feature. should be able to add individual episodes, whole seasons and whole series/movies

- Invalidate metadata after a month and make it re-fetch (still show the old meta in the UI while the new one loads)

- Auto remove metadata in LRU order after a certain threshold of storage has been reached

- Bug: there to be some cases where something is marked as watched on ted that is not synced back to stremio (some episodes I see are missing from stremio). make the two way sync push all differences to stremio, not just the new changed. This total sync from TED to Stremio can be done more sparingly, once a day for example

- Add profile pictures

## UI

- Consolidate mentions of "Shows" and "Series". They should all be "Shows"

- Add profile pictures