package main
import(
 "bytes"
 "errors"
 "strings"
 "testing"
 "github.com/yogasw/wick/plugins/service/wick_mesh/meshstate"
)
func TestDashboardFilterAndEscaping(t *testing.T){
 p,_:=meshstate.Preset(meshstate.Mobile)
 v:=buildView("/tools/wick_mesh_tool","files",p,nil)
 if len(v.Rows)!=1||v.Rows[0].Resource!="files"{t.Fatal(v)}
 var b bytes.Buffer
 v=buildView("/tools/wick_mesh_tool","<script>alert(1)</script>",p,nil)
 if e:=page.Execute(&b,v);e!=nil{t.Fatal(e)}
 if strings.Contains(b.String(),"<script>")||!strings.Contains(b.String(),"No matching resources"){t.Fatal(b.String())}
 b.Reset()
 v=buildView("/tools/wick_mesh_tool","",p,errors.New("secret-token"))
 if e:=page.Execute(&b,v);e!=nil{t.Fatal(e)}
 if strings.Contains(b.String(),"secret-token")||!strings.Contains(b.String(),"Service unavailable"){t.Fatal(b.String())}
}
