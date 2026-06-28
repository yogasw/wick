module github.com/yogasw/wick-plugins

go 1.25.0

require github.com/yogasw/wick v0.25.3

require (
	github.com/a-h/templ v0.3.1020 // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/hashicorp/go-hclog v0.14.1 // indirect
	github.com/hashicorp/go-plugin v1.6.2 // indirect
	github.com/hashicorp/yamux v0.1.1 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/oklog/run v1.0.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/grpc v1.81.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// NOTE: no `replace` here on purpose. The repo-root go.work wires this module to
// the local wick checkout for dev + CI, so this go.mod stays clean and ready to
// extract into its own repo. The require above pins the wick version a STANDALONE
// clone would use; bump it to the first release that contains pkg/plugin once
// that ships.
