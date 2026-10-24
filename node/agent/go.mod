module github.com/human-agent65535/nassimhub-node/agent

go 1.25.0

require (
	github.com/godbus/dbus/v5 v5.2.2
	github.com/human-agent65535/nassimhub-node/proto v0.0.0
	github.com/human-agent65535/nassimhub-node/xport v0.0.0
)

require golang.org/x/sys v0.44.0

replace github.com/human-agent65535/nassimhub-node/proto => ../proto

replace github.com/human-agent65535/nassimhub-node/xport => ../xport
