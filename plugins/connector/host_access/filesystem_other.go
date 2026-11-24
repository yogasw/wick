//go:build !linux

package main

import (
 "crypto/sha256"
 "encoding/hex"
 "errors"
 "sync"
)
const maxFileBytes=1<<20
var fileMu sync.Mutex
var unsupported=errors.New("secure no-follow filesystem backend is currently available on Linux/Termux only")
func readFile(string)([]byte,error){return nil,unsupported}
func digest(b []byte)string{h:=sha256.Sum256(b);return hex.EncodeToString(h[:])}
func statFile(string)(map[string]any,error){return nil,unsupported}
func listDir(string,int)([]string,bool,error){return nil,false,unsupported}
func writeNew(string,[]byte)error{return unsupported}
func replaceFile(string,[]byte,string)error{return unsupported}
func mkdir(string)error{return unsupported}
func removeFile(string,bool)error{return unsupported}
func diskUsage(string)(map[string]any,error){return nil,unsupported}
