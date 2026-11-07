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
 if response["type"]!="file.error"{t.Fatalf("directory move should be refused: %v",response)}
 if _,err:=os.Stat(filepath.Join(dest,"directory"));err!=nil{t.Fatal(err)}

 ws3:=dial(t,writable)
 q=request("file.move","directory");q["new_path"]="../escape.txt"
 sendText(t,ws3,q)
 _,response,_=receive(t,ws3)
 if response["type"]!="file.error"||response["code"]!="INVALID_PATH"{t.Fatalf("unsafe path accepted: %v",response)}
}
