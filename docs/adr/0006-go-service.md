# ADR 0006: Go for the service, reusing the Discord bridge

Status: accepted, 2026-09-10.

## Context
Open Discord Bridge already ships a Go service with a file tailer (local and
SFTP), a reconnecting RCON client, a two-mode config loader with an effective
snapshot, and a tag-driven release pipeline that builds binaries for four
platforms and publishes to the mod portal.

## Decision
The service is a Go module with the same layout. The reusable packages are
copied over unchanged. The agent loop uses the official Anthropic Go SDK.

## Consequences
One binary per platform, the same deployment menu as the bridge, and one
operator experience across the family. The wizard and the release plumbing
carry over with renames only.
