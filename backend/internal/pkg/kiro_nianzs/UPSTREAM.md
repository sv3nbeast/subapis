# nianzs Kiro engine snapshot

This directory is a source-faithful snapshot of backend/internal/pkg/kiro from:

- Repository: https://github.com/nianzs/sub2api
- Commit: d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb
- Commit date: 2026-08-10T03:06:35Z

The Go package name intentionally remains kiro; callers should import this
directory with the alias nianzskiro.

Do not edit snapshot files directly. Compatibility and Sub2API integration code
belongs outside this directory. Refresh the snapshot atomically from the pinned
upstream commit, then run the parity and gateway regression suites.
