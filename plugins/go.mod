module github.com/yogasw/wick/plugins

go 1.26.0

require (
	github.com/dop251/goja v0.0.0-20261102223426-a177d211bf60
	github.com/evanw/esbuild v0.28.2
	github.com/playwright-community/playwright-go v0.5001.0
	github.com/rs/zerolog v1.31.0
	github.com/stretchr/testify v1.11.1
	github.com/yogasw/wick v0.32.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/deckarep/golang-set/v2 v2.6.0 // indirect
	github.com/dlclark/regexp2/v2 v2.11.0 // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/go-jose/go-jose/v3 v3.0.3 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/go-stack/stack v1.8.1 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	github.com/hashicorp/go-hclog v0.14.1 // indirect
	github.com/hashicorp/go-plugin v1.6.2 // indirect
	github.com/hashicorp/yamux v0.1.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/oklog/run v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/grpc v1.81.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// This is a SEPARATE Go module living inside the wick repo (so `go build ./...`
// on wick never drags the plugins in, and each plugin pins its own wick
// version). The repo-root go.work wires it to the local wick checkout — no
// `replace` needed here. The require above is the wick version a clone resolves
// when go.work is absent; bump it to the first release that contains pkg/plugin.
