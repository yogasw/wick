package builder

import (
 "os"
 "path/filepath"
 "strings"
 "testing"

 "github.com/yogasw/wick/pkg/safeexec"
)

func TestDirtySourceKeepsRevisionInLDFlags(t *testing.T) {
 git, err := safeexec.LookPath("git")
 if err != nil { t.Skip("git unavailable") }
 dir := t.TempDir()
 run := func(args ...string) string {
  t.Helper()
  out, err := safeexec.Command(git, append([]string{"-C", dir}, args...)...).CombinedOutput()
  if err != nil { t.Fatalf("git %v: %v: %s", args, err, out) }
  return strings.TrimSpace(string(out))
 }
 run("init")
 if err := os.WriteFile(filepath.Join(dir,"VERSION"), []byte("v1.0.0\n"),0600); err != nil { t.Fatal(err) }
 run("add","VERSION")
 run("-c","user.name=test","-c","user.email=test@example.com","-c","commit.gpgsign=false","commit","-m","test")
 sha := run("rev-parse","HEAD")
 t.Setenv("WICK_BUILD_SOURCE_DIR",dir)
 t.Setenv("WICK_BUILD_COMMIT","dirty")
 t.Setenv("WICK_BUILD_REF","")
 m,ok := sourceMetadata()
 if !ok || m.Revision != sha || !m.Dirty { t.Fatalf("metadata: %+v",m) }
 flags := strings.Join(assembleLDFlags(Config{})," ")
 if !strings.Contains(flags,"app.BuildCommit="+sha) || strings.Contains(flags,"app.BuildCommit=dirty") { t.Fatal(flags) }
}
