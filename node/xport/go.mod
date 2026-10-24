module github.com/human-agent65535/nassimhub-node/xport

go 1.26.0

require (
	github.com/human-agent65535/nassimhub-node/proto v0.0.0
	github.com/quic-go/quic-go v0.64.1
)

require (
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/human-agent65535/nassimhub-node/proto => ../proto
