# AGENTS.md — internal/proxy

The configuration of tinyproxy, the egress proxy that root runs in a sandbox (D22). Read the root `AGENTS.md` and `ARCHITECTURE.md` first. Every change here is `risk-high`.

## Layout

- `proxy.go` — `Config`, `Files`, and the constants that the firewall shares: `User`, `Port`, `LoopbackAddr`, `ContainerAddr`, `ConfigPath`, `AllowlistPath`. Pure, no I/O.

## Invariants

- `Config` refuses the whole list when one host fails `policy.ValidateHost`.
- Every allowlist line is `^host$` with every dot escaped. Matching is exact and ignores case (`FilterCaseSensitive Off`); `FilterDefaultDeny Yes` refuses every other host.
- `ConnectPort 443` is the only CONNECT port. The firewall limits the proxy user to ports 80 and 443 and DNS: plain HTTP to an allowed host goes through port 80, and tinyproxy does not check the port of a plain HTTP request.
- No `Listen` line: one line cannot name both addresses. `Allow` limits the clients, and the firewall drops connections to the port that do not arrive on loopback.
- `User` and `Group` are `tinyproxy`, so the proxy never keeps root.

## Validation

`go test ./internal/proxy/...` needs no OrbStack.
