// Package apis exposes the standard Sudoku tunnel (HTTP mask + Sudoku obfuscation + AEAD)
// as a small Go API. It intentionally excludes the hybrid (Mieru) split uplink/downlink
// mode used by the CLI binary so it can be embedded easily by other projects.
//
// Key entry points:
//   - ProtocolConfig / DefaultConfig: describe all required parameters.
//   - Dial: client-side helper that connects to a Sudoku server and sends the target address.
//   - ServerHandshake: server-side helper that upgrades an accepted TCP connection and returns
//     the decrypted tunnel plus the requested target address.
//   - HandshakeError: wraps errors while preserving bytes already consumed so callers can
//     gracefully fall back to raw TCP/HTTP handling if desired.
//
// The configuration mirrors the CLI behavior: build a Sudoku table via
// sudoku.NewTable(seed, "prefer_ascii"|"prefer_entropy"), pick an AEAD (chacha20-poly1305 is
// the default), keep the key and padding settings consistent across client/server, and apply
// an optional handshake timeout on the server side.
package apis
