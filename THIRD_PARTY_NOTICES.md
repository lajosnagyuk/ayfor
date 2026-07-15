# Third-party notices

This file records the origin and licence of assets distributed in ayfor and
its built-in typewriter packages. The repository's BSD-3-Clause licence does
not replace these separate terms.

Go dependency licences are collected from the exact linked module graph into
every binary release under `third-party-licenses/go/`; CI fails if a linked
module exposes no licence or notice.

## Courier Prime Regular and Bold

- Copyright 2015 The Courier Prime Project Authors.
- Source: `quoteunquoteapps/CourierPrime`, commit
  `33d9d2ca2f56e6d348f6b84d4d10a34550f74220`, file
  `fonts/ttf/CourierPrime-Regular.ttf`.
- Bundled SHA-256:
  `72f793376f8e2841656bf21d77a5de010f2929bd6956a22ee848ad0c7eb978af`.
- The source tree also carries `fonts/ttf/CourierPrime-Bold.ttf` from that same
  commit, SHA-256
  `ff1f38786c849d1c41fa8e447960abdb2bd75fdfb0cfcdeb524fad65a5af3638`.
- Licence: SIL Open Font License 1.1. The complete text is in
  `assets/fonts/OFL.txt` and the Classic package.

## Cutive Mono Regular

- Copyright 2012 The Cutive Project Authors.
- Source: `google/fonts`, commit
  `7c38f10e1cbdc02df83ef5919d9639dcf1e3474f`, file
  `ofl/cutivemono/CutiveMono-Regular.ttf`.
- Bundled SHA-256:
  `96a36a00079058684982f61ee334323f8b501d7b68dcecd6049a4f9177e3a62c`.
- Licence: SIL Open Font License 1.1. The complete text is in the Olympia
  SM3 package.

## Special Elite Regular

- Copyright (c) 2010 by Brian J. Bonislawsky DBA Astigmatic (AOETI). All
  rights reserved.
- Source: `google/fonts`, commit
  `0c42307921ce94c085ce191020cde436f8396ec3`, file
  `apache/specialelite/SpecialElite-Regular.ttf`.
- Bundled SHA-256:
  `a776fcb4ceb8bdf03e2967688ebdad42680de5b91a7e62c17e718ae212d14bc4`.
- Licence: Apache License 2.0. The complete text is in the Olympia Splendid
  66 package.

## Olivetti Lettera 22 recording excerpts

- Work: `typewriter-olivetti-lettra-22`, a 1:46 MP3 field recording.
- Creator: keithpeter (Freesound), served by Pixabay's
  `freesound_community` account.
- Direct source:
  https://pixabay.com/sound-effects/typewriter-olivetti-lettra-22-20217/
- Downloaded: 2026-07-02.
- Licence at download: Pixabay Content License. The binding terms and summary
  are at https://pixabay.com/service/terms/ and
  https://pixabay.com/service/license-summary/.
- Use in ayfor: five isolated 140 ms strikes were peak-selected, trimmed,
  faded, filtered, compressed and peak-normalised, then encoded as mono PCM16
  at 44.1 kHz. Package WAV files are lossless containers of those processed
  excerpts. They are integrated components of the application and are not
  offered as a substitute for or standalone copy of the source recording.
- Detailed processing record and hashes: `internal/sound/samples/SOURCE.md`.

The Olympia packages use these same Olivetti-derived excerpts. Their
manifests deliberately say `fidelity: inspired`; they do not claim the audio
was recorded from an Olympia machine.
