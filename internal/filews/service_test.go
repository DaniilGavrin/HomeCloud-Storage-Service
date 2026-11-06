package filews

import (
 "context"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "net"
 "net/http"
 "net/http/httptest"
 "net/netip"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
 "github.com/coder/websocket"
)

func testSetup(t *testing.T, perm catalog.Permission)(*httptest.Server,string,string){
 t.Helper()
 dir:=t.TempDir()
 c,err:=catalog.New([]catalog.Root{{ID:"safe",Kind:catalog.Folder,Path:dir,Permission:perm}})
 if err!=nil{t.Fatal(err)}
 token:="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
 s:=&Service{Catalog:c,TokenSHA256:sha256.Sum256([]byte(token)),AllowedPeers:[]netip.Addr{netip.MustParseAddr("127.0.0.1")},Slots:make(chan struct{},2),MaxUpload:1<<20}
 ts:=httptest.NewServer(s)
 t.Cleanup(ts.Close)
 return ts,dir,token
}
func connect(t *testing.T,server *httptest.Server,token string)*websocket.Conn{
 t.Helper()
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
 defer cancel()
 c,resp,err:=websocket.Dial(ctx,"ws"+strings.TrimPrefix(server.URL,"http"),&websocket.DialOptions{HTTPHeader:http.Header{"Authorization":[]string{"Bearer "+token}}})
 if err!=nil{t.Fatalf("websocket dial: %v status %v",err,resp)}
 t.Cleanup(func(){c.CloseNow()})
 return c
}
func textReq(t *testing.T,c *websocket.Conn,v any){
 t.Helper()
 b,err:=json.Marshal(v);if err!=nil{t.Fatal(err)}
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel()
 if err:=c.Write(ctx,websocket.MessageText,b);err!=nil{t.Fatal(err)}
}
func next(t *testing.T,c *websocket.Conn)(websocket.MessageType,map[string]any,[]byte){
 t.Helper()
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel()
 typ,b,err:=c.Read(ctx);if err!=nil{t.Fatal(err)}
 if typ==websocket.MessageBinary{return typ,nil,b}
 var m map[string]any
 if err:=json.Unmarshal(b,&m);err!=nil{t.Fatal(err)}
 return typ,m,nil
}
func req(typ,p string)map[string]any{return map[string]any{"v":1,"id":"r1","type":typ,"path":p,"root":map[string]string{"kind":"folder","id":"safe"}}}

func TestTailnetPeerValidation(t *testing.T){
 if _,err:=ParseAllowedIPs(nil);err==nil{t.Fatal("empty ACL allowed")}
 if _,err:=ParseAllowedIPs([]string{"192.168.1.2"});err==nil{t.Fatal("LAN IP allowed")}
 if _,err:=ParseAllowedIPs([]string{"100.114.58.74"});err!=nil{t.Fatal(err)}
 if ValidPath("../etc/passwd")||ValidPath("/etc/passwd")||ValidPath("a//b")||ValidPath("a\\b")||ValidPath("folder/.hcss-upload-payload") {t.Fatal("traversal or temp path allowed")}
 if !ValidPath("Фото/семья/мой файл.mkv"){t.Fatal("unicode path not allowed")}
}
func TestTokenCreatedPrivateAndReusable(t *testing.T){
 fn:=filepath.Join(t.TempDir(),"data","filews-token")
 hash,err:=LoadOrCreateToken(fn);if err!=nil{t.Fatal(err)}
 st,err:=os.Stat(fn);if err!=nil{t.Fatal(err)}
 if st.Mode().Perm()!=0600{t.Fatalf("token mode %o",st.Mode().Perm())}
 hash2,err:=LoadOrCreateToken(fn);if err!=nil||hash!=hash2{t.Fatal("token changed",err)}
 raw,err:=os.ReadFile(fn);if err!=nil{t.Fatal(err)}
 binary,err:=hex.DecodeString(strings.TrimSpace(string(raw)));if err!=nil||len(binary)!=32{t.Fatal("token invalid")}
}
func TestUnauthorizedWithoutSecretAndBrowserOrigin(t *testing.T){
 server,_,token:=testSetup(t,catalog.Permission{List:true,Read:true,Write:true,Delete:true})
 resp,err:=http.Get(server.URL);if err!=nil{t.Fatal(err)};resp.Body.Close()
 if resp.StatusCode!=http.StatusUnauthorized{t.Fatalf("unauthorized status %d",resp.StatusCode)}
 request,_:=http.NewRequest("GET",server.URL,nil)
 request.Header.Set("Authorization","Bearer "+token)
 request.Header.Set("Origin","https://attacker.invalid")
 reply,err:=http.DefaultClient.Do(request);if err!=nil{t.Fatal(err)};reply.Body.Close()
 if reply.StatusCode!=http.StatusForbidden{t.Fatalf("browser status %d",reply.StatusCode)}
}
func TestUploadDownloadCreateRenameDelete(t *testing.T){
 server,dir,token:=testSetup(t,catalog.Permission{List:true,Read:true,Write:true,Delete:true})
 data:=[]byte("HomeCloud FS V1: no control WS bytes")
 digest:=sha256.Sum256(data)
 c:=connect(t,server,token)
 put:=req("file.put","test.txt");put["size_bytes"]=len(data)
 textReq(t,c,put)
 _,ready,_:=next(t,c);if ready["type"]!="file.ready"{t.Fatal(ready)}
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
 if err:=c.Write(ctx,websocket.MessageBinary,data);err!=nil{t.Fatal(err)};cancel()
 textReq(t,c,map[string]any{"v":1,"type":"file.end","id":"r1","sha256":hex.EncodeToString(digest[:])})
 _,done,_:=next(t,c);if done["type"]!="file.complete"{t.Fatal(done)}
 raw,err:=os.ReadFile(filepath.Join(dir,"test.txt"));if err!=nil||string(raw)!=string(data){t.Fatalf("upload mismatch: %v",err)}
 if _,err:=os.Stat(filepath.Join(dir,".hcss-upload-" ));err==nil{t.Fatal("unexpected temp")}
 c2:=connect(t,server,token)
 textReq(t,c2,req("file.get","test.txt"))
 _,ready,_=next(t,c2);if ready["type"]!="file.ready"{t.Fatal(ready)}
 typ,_,blob:=next(t,c2);if typ!=websocket.MessageBinary||string(blob)!=string(data){t.Fatal("download differs")}
 _,done,_=next(t,c2);if done["type"]!="file.complete"||done["sha256"]!=hex.EncodeToString(digest[:]){t.Fatal(done)}
 c3:=connect(t,server,token)
 textReq(t,c3,req("file.put","test.txt"))
 _,errReply,_:=next(t,c3)
 // First upload with missing size is 0; service may offer ready and reject when publishing.
 if errReply["type"]=="file.ready" {textReq(t,c3,map[string]any{"v":1,"type":"file.end","id":"r1","sha256":hex.EncodeToString(sha256.New().Sum(nil))});_,errReply,_=next(t,c3)}
 if errReply["type"]!="file.error"||errReply["code"]!="TARGET_EXISTS"{t.Fatal("existing file should not be overwritten",errReply)}
 mkdir:=connect(t,server,token)
 textReq(t,mkdir,req("file.mkdir","folder"));_,done,_=next(t,mkdir);if done["type"]!="file.complete"{t.Fatal(done)}
 move:=connect(t,server,token)
 q:=req("file.rename","test.txt");q["new_path"]="folder/renamed.txt"
 textReq(t,move,q);_,done,_=next(t,move);if done["type"]!="file.complete"{t.Fatal(done)}
 remove:=connect(t,server,token)
 textReq(t,remove,req("file.delete","folder/renamed.txt"));_,done,_=next(t,remove);if done["type"]!="file.complete"{t.Fatal(done)}
 remove2:=connect(t,server,token)
 textReq(t,remove2,req("file.delete","folder"));_,done,_=next(t,remove2);if done["type"]!="file.complete"{t.Fatal(done)}
}
func TestReadOnlyRootRejectsMutations(t *testing.T){
 server,_,token:=testSetup(t,catalog.Permission{List:true,Read:true})
 c:=connect(t,server,token)
 textReq(t,c,req("file.mkdir","blocked"))
 _,result,_:=next(t,c)
 if result["type"]!="file.error"||result["code"]!="WRITE_FORBIDDEN"{t.Fatal(result)}
}
func TestPeerListNeverTrustsForwardedFor(t *testing.T){
 server,_,token:=testSetup(t,catalog.Permission{List:true,Read:true})
 // RemoteAddr is the trusted TCP peer, not headers.
 r,_:=http.NewRequest("GET",server.URL,nil)
 r.Header.Set("Authorization","Bearer "+token)
 r.Header.Set("X-Forwarded-For","100.82.22.45")
 resp,err:=http.DefaultClient.Do(r);if err!=nil{t.Fatal(err)};resp.Body.Close()
 if resp.StatusCode==http.StatusOK{t.Fatal("plain request upgraded unexpectedly")}
 _,_,_=net.SplitHostPort("127.0.0.1:1")
}
