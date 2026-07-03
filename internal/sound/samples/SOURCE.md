# Sample source

`strike-01.pcm` .. `strike-05.pcm` are extracted from:

    freesound_community-typewriter-olivetti-lettra-22-20217.mp3
    Downloaded from Pixabay, 2026-07-02.
    License: Pixabay License (https://pixabay.com/service/license-summary/)
    Free for commercial and non-commercial use, no attribution required.

## Processing

Fifteen candidate single-keystroke hits were located automatically (peak
picking on the amplitude envelope, kept only if the next onset was at
least 140ms later, so the extracted window doesn't bleed into the next
strike). Each was trimmed to 140ms with a 0.5ms fade-in and 14ms fade-out,
then run through: high-pass at 100Hz (cut handling rumble), a body boost
around 220Hz, a presence boost around 3.2kHz (the "stab"), light
compression, a low-pass at 12kHz (tame mp3 hiss), and peak-normalized.

Two of the fifteen (originally named strike-06 and strike-13 in the
candidate batch) were rejected on a listen as not clean single hits,
leaving 13.

## Down-select to 5 (2026-07-02, second pass)

With all 13 in the bank, typing fast made the mix sound inconsistent -
too many distinct mic/room characters bunched up on rapid strikes. Kept
the 5 whose spectral centroid clustered tightest together (computed via
FFT over each clip, ranked by distance from the group median): the
outliers were noticeably duller (centroid ~4.1-4.5kHz vs ~5.1-5.5kHz for
the kept set). The current `strike-01.pcm`..`strike-05.pcm` are,
respectively, candidates that were previously numbered 04, 07, 08, 10, 12
in the 13-sample set.

Runtime now adds deterministic pitch modulation (see `sound.go`): rather
than relying on raw sample variety alone, each of these 5 base samples is
resampled at strike time by a rate derived from the strike's ink weight,
the same way the old synthesized thock got its 9 pitch/length variants.
This is what gives back-to-back strikes their variety now, not the base
sample count - so 5 well-matched recordings plus modulation reads as one
consistent machine instead of a grab bag.

Format: mono PCM16 little-endian at 44100 Hz, headerless - the same wire
format `Scale()` already expects, so no decoder is needed at runtime.

## Redistribution position (for the open-source release)

The Pixabay Content License permits use of content in software and
derived works, and restricts redistributing content on a standalone
basis (e.g. re-uploading it to another stock library). These files are
not the recording: they are five 140 ms excerpts, EQ'd, compressed,
trimmed and re-encoded, embedded as a component of a program - the
ordinary "integrated into a product" use the license is written to
allow. They are not offered as standalone audio, and anyone wanting the
source recording should get it from Pixabay. The code of this
repository is licensed separately (see LICENSE at the repo root); that
license does not and cannot cover these samples.
