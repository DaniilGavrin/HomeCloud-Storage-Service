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

// Files are not executable. Parent directories require x to be traversable.
// The mode must not depend on the Storage Service's process umask.
func TestNewUploadAndDirectoryPermissions(t *testing.T) {
 server, dir := setup(t, catalog.Permission{List:true, Read:true, Write:true, Delete:true})
 folder := dial(t,server)
 sendText(t,folder,request("file.mkdir","shared"))
 _,reply,_:=receive(t,folder)
 if reply["type"]!="file.complete" {t.Fatal(reply)}
 directory,err:=os.Stat(filepath.Join(dir,"shared"))
 if err!=nil {t.Fatal(err)}
 if got:=directory.Mode().Perm();got!=0o777 {
  t.Fatalf("new directory mode=%#o, want 0777",got)
 }

 fileData:=[]byte("file permissions and SHA-256")
 hash:=sha256.Sum256(fileData)
 upload:=dial(t,server)
 q:=request("file.put","shared/test.txt")
 q["size_bytes"]=len(fileData)
 sendText(t,upload,q)
 _,ready,_:=receive(t,upload)
 if ready["type"]!="file.ready" {t.Fatal(ready)}
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
 if err:=upload.Write(ctx,websocket.MessageBinary,fileData);err!=nil {t.Fatal(err)}
 cancel()
 sendText(t,upload,map[string]any{"v":1,"type":"file.end","id":"r1","sha256":hex.EncodeToString(hash[:])})
 _,done,_:=receive(t,upload)
 if done["type"]!="file.complete" {t.Fatal(done)}
 stat,err:=os.Stat(filepath.Join(dir,"shared","test.txt"))
 if err!=nil {t.Fatal(err)}
 if got:=stat.Mode().Perm();got!=0o666 {
  t.Fatalf("new regular file mode=%#o, want 0666 with NO execute bits",got)
 }
}

// An incorrect upload hash must be classified as retriable and must never
// publish the destination file, leaving the client free to retry cleanly.
func TestUploadChecksumMismatchDoesNotPublishFile(t *testing.T) {
 server,dir:=setup(t,catalog.Permission{List:true,Read:true,Write:true})
 ws:=dial(t,server)
 bytes:=[]byte("integrity test")
 request:=request("file.put","retry.txt")
 request["size_bytes"]=len(bytes)
 sendText(t,ws,request)
 _,ready,_:=receive(t,ws)
 if ready["type"]!="file.ready" {t.Fatal(ready)}
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
 if err:=ws.Write(ctx,websocket.MessageBinary,bytes);err!=nil {t.Fatal(err)}
 cancel()
 sendText(t,ws,map[string]any{"v":1,"type":"file.end","id":"r1","sha256":strings.Repeat("0",64)})
 _,result,_:=receive(t,ws)
 if result["type"]!="file.error" || result["code"]!="CHECKSUM_MISMATCH" {
  t.Fatalf("expected retryable CHECKSUM_MISMATCH, got %v",result)
 }
 if _,err:=os.Stat(filepath.Join(dir,"retry.txt"));!os.IsNotExist(err){
  t.Fatalf("failed integrity upload published destination: %v",err)
 }
 files,err:=os.ReadDir(dir);if err!=nil {t.Fatal(err)}
 for _,file:=range files {
  if strings.HasPrefix(file.Name(),".hcss-upload-"){
    t.Fatalf("orphaned temporary upload: %s",file.Name())
  }
 }
}

// Moving is a write action and must also work in HC SS roots with deletion
// disabled. No temporary copies or file bytes traverse Backend or UI.
func TestMoveFileBetweenDirectoriesWithoutDeletePermission(t *testing.T) {
 server,dir:=setup(t,catalog.Permission{List:true,Read:true,Write:true,Delete:false})
 if err:=os.Mkdir(filepath.Join(dir,"source"),0755);err!=nil{t.Fatal(err)}
 if err:=os.Mkdir(filepath.Join(dir,"Movie"),0755);err!=nil{t.Fatal(err)}
 old:=filepath.Join(dir,"source","Глянцевый логотип.png")
 payload:=[]byte("a file moved without copying its content")
 if err:=os.WriteFile(old,payload,0666);err!=nil{t.Fatal(err)}
 ws:=dial(t,server)
 q:=request("file.move","source/Глянцевый логотип.png")
 q["new_path"]="Movie/Глянцевый логотип.png"
 sendText(t,ws,q)
 _,response,_:=receive(t,ws)
 if response["type"]!="file.complete"{t.Fatalf("move rejected: %v",response)}
 destination:=filepath.Join(dir,"Movie","Глянцевый логотип.png")
 got,err:=os.ReadFile(destination)
 if err!=nil||string(got)!=string(payload){t.Fatalf("wrong moved content: %v",err)}
 if _,err:=os.Lstat(old);!os.IsNotExist(err){t.Fatalf("original path still exists: %v",err)}
}

// A target collision must not overwrite or delete either file.
func TestMoveRejectsExistingDestination(t *testing.T) {
 server,dir:=setup(t,catalog.Permission{List:true,Read:true,Write:true})
 if err:=os.Mkdir(filepath.Join(dir,"destination"),0755);err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(filepath.Join(dir,"a.txt"),[]byte("from"),0600);err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(filepath.Join(dir,"destination","a.txt"),[]byte("already here"),0600);err!=nil{t.Fatal(err)}
 ws:=dial(t,server)
 q:=request("file.move","a.txt");q["new_path"]="destination/a.txt"
 sendText(t,ws,q)
 _,response,_:=receive(t,ws)
 if response["type"]!="file.error"||response["code"]!="TARGET_EXISTS"{t.Fatalf("expected collision: %v",response)}
 src,err:=os.ReadFile(filepath.Join(dir,"a.txt"));if err!=nil||string(src)!="from"{t.Fatal("source changed",err)}
 dst,err:=os.ReadFile(filepath.Join(dir,"destination","a.txt"));if err!=nil||string(dst)!="already here"{t.Fatal("destination overwritten",err)}
}

// Move must not provide a hidden delete permission, move directories, or
// escape the configured HC SS root through traversal/symlinks.
func TestMoveRejectsInvalidRequestsAndReadOnlyRoot(t *testing.T) {
 readonly,dir:=setup(t,catalog.Permission{List:true,Read:true})
 if err:=os.WriteFile(filepath.Join(dir,"a.txt"),[]byte("x"),0600);err!=nil{t.Fatal(err)}
 ws:=dial(t,readonly)
 q:=request("file.move","a.txt");q["new_path"]="new.txt"
 sendText(t,ws,q)
 _,response,_:=receive(t,ws)
 if response["type"]!="file.error"||response["code"]!="WRITE_FORBIDDEN"{t.Fatalf("read-only move accepted: %v",response)}

 writable,dest:=setup(t,catalog.Permission{List:true,Read:true,Write:true})
 if err:=os.Mkdir(filepath.Join(dest,"directory"),0755);err!=nil{t.Fatal(err)}
 ws2:=dial(t,writable)
 q=request("file.move","directory");q["new_path"]="renamed"
 sendText(t,ws2,q)
 _,response,_=receive(t,ws2)
 if response["type"]!="file.complete"{t.Fatalf("directory move rejected: %v",response)}
 if _,err:=os.Stat(filepath.Join(dest,"renamed"));err!=nil{t.Fatal(err)}

 ws3:=dial(t,writable)
 q=request("file.move","directory");q["new_path"]="../escape.txt"
 sendText(t,ws3,q)
 _,response,_=receive(t,ws3)
 if response["type"]!="file.error"||response["code"]!="INVALID_PATH"{t.Fatalf("unsafe path accepted: %v",response)}
}


func TestWebOSCopyFilesAndDirectories(t *testing.T) {
 srv,dir:=setup(t,catalog.Permission{List:true,Read:true,Write:true})
 if err:=os.MkdirAll(filepath.Join(dir,"a","deep"),0755);err!=nil{t.Fatal(err)}
 if err:=os.Mkdir(filepath.Join(dir,"other"),0755);err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(filepath.Join(dir,"a","deep","фильм.mkv"),[]byte("immutable content"),0644);err!=nil{t.Fatal(err)}
 for _,tc:=range []struct{op,src,dst string}{
  {"file.copy","a/deep/фильм.mkv","other/фильм.mkv"},
  {"file.copy","a","a-copy"},
 }{
  ws:=dial(t,srv);q:=request(tc.op,tc.src);q["new_path"]=tc.dst
  sendText(t,ws,q);_,reply,_:=receive(t,ws)
  if reply["type"]!="file.complete"{t.Fatalf("%s: %v",tc.op,reply)}
 }
 for _,p:=range []string{"a/deep/фильм.mkv","other/фильм.mkv","a-copy/deep/фильм.mkv"}{
  data,err:=os.ReadFile(filepath.Join(dir,filepath.FromSlash(p)));if err!=nil||string(data)!="immutable content"{t.Fatal(p,err)}
 }
 // An independent copy has a different inode: editing it never edits source.
 if err:=os.WriteFile(filepath.Join(dir,"other","фильм.mkv"),[]byte("changed"),0644);err!=nil{t.Fatal(err)}
 src,_:=os.ReadFile(filepath.Join(dir,"a","deep","фильм.mkv"));if string(src)!="immutable content"{t.Fatal("hard-linked copy")}
 for _,p:=range []string{"a-copy","a-copy/deep"}{
  st,err:=os.Stat(filepath.Join(dir,filepath.FromSlash(p)));if err!=nil||st.Mode().Perm()!=0777{t.Fatalf("%s mode: %v %v",p,st,err)}
 }
 st,err:=os.Stat(filepath.Join(dir,"a-copy","deep","фильм.mkv"));if err!=nil||st.Mode().Perm()!=0666{t.Fatal("file mode",err)}
}
func TestWebOSMoveFoldersAndRejectUnsafeOperations(t *testing.T){
 srv,dir:=setup(t,catalog.Permission{List:true,Read:true,Write:true})
 if err:=os.MkdirAll(filepath.Join(dir,"a","inner"),0755);err!=nil{t.Fatal(err)}
 if err:=os.Mkdir(filepath.Join(dir,"dest"),0755);err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(filepath.Join(dir,"a","inner","test"),[]byte("ok"),0644);err!=nil{t.Fatal(err)}
 query:=func(action,old,new string)map[string]any{
  ws:=dial(t,srv);q:=request(action,old);q["new_path"]=new;sendText(t,ws,q);_,m,_:=receive(t,ws);return m
 }
 for _,action:=range []string{"file.copy","file.move"} {
  if got:=query(action,"a","a/inner/loop");got["type"]!="file.error"{t.Fatal("self destination",action,got)}
 }
 if got:=query("file.move","a","dest");got["code"]!="TARGET_EXISTS"{t.Fatalf("overwrite existing dir %v",got)}
 if got:=query("file.move","a","new-dir");got["type"]!="file.complete"{t.Fatal(got)}
 if _,err:=os.Stat(filepath.Join(dir,"a"));!os.IsNotExist(err){t.Fatalf("source directory still exists %v",err)}
 if got,_:=os.ReadFile(filepath.Join(dir,"new-dir","inner","test"));string(got)!="ok"{t.Fatal("lost moved subtree")}
 if err:=os.Symlink(filepath.Join(dir,"new-dir"),filepath.Join(dir,"alias"));err==nil {
  if got:=query("file.copy","alias","copy");got["type"]!="file.error"{t.Fatal("copied symlink",got)}
 }
 if got:=query("file.touch","empty.txt","");got["type"]!="file.complete"{t.Fatal(got)}
 if got:=query("file.touch","empty.txt","");got["code"]!="TARGET_EXISTS"{t.Fatal(got)}
}
func TestWebOSCopyPermissionAndRollback(t *testing.T){
 srv,dir:=setup(t,catalog.Permission{List:true,Read:true,Write:true})
 if err:=os.Mkdir(filepath.Join(dir,"a"),0755);err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(filepath.Join(dir,"a","src.txt"),[]byte("source"),0644);err!=nil{t.Fatal(err)}
 if err:=os.Symlink("src.txt",filepath.Join(dir,"a","link"));err!=nil{t.Fatal(err)}
 ws:=dial(t,srv);q:=request("file.copy","a");q["new_path"]="b"
 sendText(t,ws,q);_,reply,_:=receive(t,ws)
 if reply["type"]!="file.error"{t.Fatalf("copied symlink without rejection: %v",reply)}
 if _,err:=os.Stat(filepath.Join(dir,"b"));!os.IsNotExist(err){t.Fatal("partial published",err)}
 names,err:=os.ReadDir(dir);if err!=nil{t.Fatal(err)}
 for _,e:=range names{if strings.HasPrefix(e.Name(),".hcss-copy-"){t.Fatal("orphan temp",e.Name())}}
}

func TestWebOSCopyProgressAndLargeFile(t *testing.T) {
 srv,dir:=setup(t,catalog.Permission{List:true,Read:true,Write:true})
 payload:=make([]byte,5<<20)
 for i:=range payload {payload[i]=byte(i)}
 if err:=os.WriteFile(filepath.Join(dir,"original.bin"),payload,0666);err!=nil{t.Fatal(err)}
 ws:=dial(t,srv);q:=request("file.copy","original.bin");q["new_path"]="copied.bin"
 sendText(t,ws,q)
 var progressSeen bool
 for {
  _,m,_:=receive(t,ws)
  if m["type"]=="file.progress" {
   progressSeen=true
   if got,_:=m["completed_bytes"].(float64);got<=0 {t.Fatalf("invalid copy progress: %v",m)}
   continue
  }
  if m["type"]!="file.complete"{t.Fatalf("bad copy result: %v",m)}
  break
 }
 if !progressSeen{t.Fatal("large copy did not report progress")}
 output,err:=os.ReadFile(filepath.Join(dir,"copied.bin"))
 if err!=nil||len(output)!=len(payload){t.Fatalf("copy output size: %v",err)}
 for i:=range payload {if output[i]!=payload[i]{t.Fatalf("copy differs at byte %d",i)}}
}
