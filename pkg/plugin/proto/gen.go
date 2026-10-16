package proto

//go:generate protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative connector.proto
//go:generate protoc --go_out=../../.. --go_opt=module=github.com/yogasw/wick --go-grpc_out=../../.. --go-grpc_opt=module=github.com/yogasw/wick host.proto
