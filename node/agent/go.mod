module github.com/human-agent65535/nassimhub-node/agent

go 1.26.0

require (
	github.com/godbus/dbus/v5 v5.2.2
	github.com/human-agent65535/nassimhub-node/proto v0.0.0
	github.com/human-agent65535/nassimhub-node/xport v0.0.0
)

require (
	github.com/quic-go/quic-go v0.64.1 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0
)

replace github.com/human-agent65535/nassimhub-node/proto => ../proto

replace github.com/human-agent65535/nassimhub-node/xport => ../xport
