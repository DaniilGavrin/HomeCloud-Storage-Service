package filews

import (
 "context"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"
 "github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
 "github.com/coder/websocket"
)
func setup(t *testing.T, perm catalog.Permission)(*httptest.Server,string){
 t.Helper()
 dir:=t.TempDir()
 c,err:=catalog.New([]catalog.Root{{ID:"safe",Kind:catalog.Folder,Path:dir,Permission:perm}})
 if err!=nil{t.Fatal(err)}
 s:=httptest.NewServer(&Service{Catalog:c,Slots:make(chan struct{},2),MaxUpload:1<<20})
 t.Cleanup(s.Close)
 return s,dir
}
func dial(t *testing.T,server *httptest.Server)*websocket.Conn{
 t.Helper()
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel()
 ws,response,err:=websocket.Dial(ctx,"ws"+strings.TrimPrefix(server.URL,"http"),nil)
 if err!=nil{t.Fatalf("file WS requires no token: %v status %v",err,response)}
 t.Cleanup(func(){ws.CloseNow()})
 return ws
}
func sendText(t *testing.T,ws *websocket.Conn,v any){
 t.Helper(); data,err:=json.Marshal(v);if err!=nil{t.Fatal(err)}
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel()
 if err:=ws.Write(ctx,websocket.MessageText,data);err!=nil{t.Fatal(err)}
}
func receive(t *testing.T,ws *websocket.Conn)(websocket.MessageType,map[string]any,[]byte){
 t.Helper();ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel()
 typ,data,err:=ws.Read(ctx);if err!=nil{t.Fatal(err)}
 if typ==websocket.MessageBinary{return typ,nil,data}
 var m map[string]any;if err:=json.Unmarshal(data,&m);err!=nil{t.Fatal(err)}
 return typ,m,nil
}
func request(action,p string)map[string]any {
 return map[string]any{"v":1,"id":"r1","type":action,"path":p,"root":map[string]string{"kind":"folder","id":"safe"}}
}
func TestNoKeysAndBrowserOriginDenied(t *testing.T){
 s,_:=setup(t,catalog.Permission{List:true,Read:true,Write:true,Delete:true})
 ws:=dial(t,s)
 sendText(t,ws,request("file.mkdir","created"))
 _,done,_:=receive(t,ws);if done["type"]!="file.complete"{t.Fatal(done)}
 r,_:=http.NewRequest("GET",s.URL,nil);r.Header.Set("Origin","https://attacker.invalid")
 response,err:=http.DefaultClient.Do(r);if err!=nil{t.Fatal(err)};response.Body.Close()
 if response.StatusCode!=http.StatusForbidden{t.Fatalf("unexpected origin response %d",response.StatusCode)}
}
func TestFileTransferOperationsAndPermissions(t *testing.T){
 s,dir:=setup(t,catalog.Permission{List:true,Read:true,Write:true,Delete:true})
 data:=[]byte("HomeCloud keyless file transfer"); hash:=sha256.Sum256(data);digest:=hex.EncodeToString(hash[:])
 ws:=dial(t,s); q:=request("file.put","test.txt");q["size_bytes"]=len(data);sendText(t,ws,q)
 _,ready,_:=receive(t,ws);if ready["type"]!="file.ready"{t.Fatal(ready)}
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
 if err:=ws.Write(ctx,websocket.MessageBinary,data);err!=nil{t.Fatal(err)};cancel()
 sendText(t,ws,map[string]any{"v":1,"id":"r1","type":"file.end","sha256":digest})
 _,done,_:=receive(t,ws);if done["type"]!="file.complete"||done["sha256"]!=digest{t.Fatal(done)}
 raw,err:=os.ReadFile(filepath.Join(dir,"test.txt"));if err!=nil||string(raw)!=string(data){t.Fatal("bad upload",err)}
 download:=dial(t,s);sendText(t,download,request("file.get","test.txt"))
 _,ready,_=receive(t,download);if ready["type"]!="file.ready"{t.Fatal(ready)}
 typ,_,chunk:=receive(t,download);if typ!=websocket.MessageBinary||string(chunk)!=string(data){t.Fatal("bad download")}
 _,done,_=receive(t,download);if done["sha256"]!=digest{t.Fatal("bad checksum")}
 mkdir:=dial(t,s);sendText(t,mkdir,request("file.mkdir","folder"));_,done,_=receive(t,mkdir)
 if done["type"]!="file.complete"{t.Fatal(done)}
 move:=dial(t,s);q=request("file.rename","test.txt");q["new_path"]="folder/renamed.txt";sendText(t,move,q);_,done,_=receive(t,move);if done["type"]!="file.complete"{t.Fatal(done)}
 remove:=dial(t,s);sendText(t,remove,request("file.delete","folder/renamed.txt"));_,done,_=receive(t,remove);if done["type"]!="file.complete"{t.Fatal(done)}
 delDir:=dial(t,s);sendText(t,delDir,request("file.delete","folder"));_,done,_=receive(t,delDir);if done["type"]!="file.complete"{t.Fatal(done)}
}
func TestReadonlyRootsAndPaths(t *testing.T){
 s,_:=setup(t,catalog.Permission{List:true,Read:true})
 ws:=dial(t,s);sendText(t,ws,request("file.mkdir","blocked"))
 _,done,_:=receive(t,ws);if done["type"]!="file.error"||done["code"]!="WRITE_FORBIDDEN"{t.Fatal(done)}
 for _,path:=range []string{"../etc/passwd","/etc/passwd","a//b","a\\b","folder/.hcss-upload-evil"}{
  if ValidPath(path){t.Fatalf("unsafe path accepted: %q",path)}
 }
 if !ValidPath("Фото/семья/мой файл.mkv"){t.Fatal("unicode path rejected")}
}
