// Package filews provides the root-confined file data plane.
// Listeners are automatic on private network interfaces; firewall policy
// governs reachability. No keys or per-client IP lists are used.
// File bytes never traverse Backend's control WebSocket.
package filews

import (
 "context"
 "crypto/rand"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "errors"
 "io"
 "net/http"
 "os"
 "path"
 "strings"
 "time"

 "github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
 "github.com/coder/websocket"
)

const ChunkBytes = 128 << 10
const DefaultMaxUpload = int64(32 << 30)

// The receiver reports a stable error code so the native desktop client can
// retry only integrity failures, not permission or filesystem errors.
var errChecksumMismatch = errors.New("CHECKSUM_MISMATCH")

type Service struct {
 Catalog *catalog.Catalog
 MaxUpload int64
 Slots chan struct{}
}

type Request struct {
 V int `json:"v"`
 ID string `json:"id"`
 Type string `json:"type"`
 Root catalog.Selector `json:"root"`
 Path string `json:"path"`
 NewPath string `json:"new_path,omitempty"`
 SizeBytes int64 `json:"size_bytes,omitempty"`
 SHA256 string `json:"sha256,omitempty"`
}

func ValidPath(p string) bool {
 if p==""||len(p)>1800||strings.HasPrefix(p,"/")||strings.ContainsAny(p,"\\:\x00")||strings.Contains(p,"//")||strings.HasSuffix(p,"/"){return false}
 for _,seg:=range strings.Split(p,"/") {
  if seg==""||seg=="."||seg==".."||strings.HasPrefix(seg,".hcss-upload-"){return false}
 }
 return true
}
func randomName() (string,error) {
 var b [16]byte
 if _,err:=rand.Read(b[:]);err!=nil{return "",err}
 return ".hcss-upload-"+hex.EncodeToString(b[:]),nil
}
func (s *Service) ServeHTTP(w http.ResponseWriter,r *http.Request){
 if s==nil||s.Catalog==nil||r.Method!=http.MethodGet {http.Error(w,"unavailable",http.StatusServiceUnavailable);return}
 // Reject browser-originated cross-site file mutations. No setup or keys.
 if r.Header.Get("Origin")!="" {http.Error(w,"forbidden",http.StatusForbidden);return}
 if s.Slots!=nil {select{case s.Slots<-struct{}{}:defer func(){<-s.Slots}();default:http.Error(w,"busy",http.StatusTooManyRequests);return}}
 conn,err:=websocket.Accept(w,r,&websocket.AcceptOptions{InsecureSkipVerify:true})
 if err!=nil{return}
 defer conn.CloseNow()
 conn.SetReadLimit(ChunkBytes+4096)
 ctx,cancel:=context.WithTimeout(r.Context(),8*time.Hour)
 defer cancel()
 typ,b,err:=read(ctx,conn)
 if err!=nil || typ!=websocket.MessageText {fail(conn,"","INVALID_REQUEST");return}
 var q Request
 if json.Unmarshal(b,&q)!=nil||q.V!=1||len(q.ID)<1||len(q.ID)>128||!ValidPath(q.Path) {
  fail(conn,q.ID,"INVALID_REQUEST");return
 }
 root,err:=s.Catalog.Root(q.Root)
 if err!=nil {fail(conn,q.ID,"ROOT_UNAVAILABLE");return}
 folder,err:=os.OpenRoot(root.Path)
 if err!=nil {fail(conn,q.ID,"ROOT_UNAVAILABLE");return}
 defer folder.Close()
 var opErr error
 switch q.Type {
 case "file.get":
  if !root.Read {fail(conn,q.ID,"READ_FORBIDDEN");return}
  opErr=s.download(ctx,conn,folder,q)
 case "file.put":
  if !root.Write {fail(conn,q.ID,"WRITE_FORBIDDEN");return}
  opErr=s.upload(ctx,conn,folder,q)
 case "file.mkdir":
  if !root.Write {fail(conn,q.ID,"WRITE_FORBIDDEN");return}
  opErr=s.mkdir(folder,q.Path)
  if opErr==nil {opErr=write(ctx,conn,map[string]any{"v":1,"type":"file.complete","id":q.ID})}
 case "file.delete":
  if !root.Delete {fail(conn,q.ID,"DELETE_FORBIDDEN");return}
  opErr=s.delete(folder,q.Path)
  if opErr==nil {opErr=write(ctx,conn,map[string]any{"v":1,"type":"file.complete","id":q.ID})}
 case "file.rename":
  if !root.Write||!root.Delete {fail(conn,q.ID,"RENAME_FORBIDDEN");return}
  if !ValidPath(q.NewPath)||q.NewPath==q.Path {fail(conn,q.ID,"INVALID_PATH");return}
  opErr=s.rename(folder,q.Path,q.NewPath)
  if opErr==nil {opErr=write(ctx,conn,map[string]any{"v":1,"type":"file.complete","id":q.ID})}
 case "file.move":
  // Moving a regular file is a write operation, even if file.delete is
  // disabled. Never replace an existing destination or leave the root.
  if !root.Write {fail(conn,q.ID,"WRITE_FORBIDDEN");return}
  if !ValidPath(q.NewPath)||q.NewPath==q.Path {fail(conn,q.ID,"INVALID_PATH");return}
  opErr=s.rename(folder,q.Path,q.NewPath)
  if opErr==nil {opErr=write(ctx,conn,map[string]any{"v":1,"type":"file.complete","id":q.ID})}
 default:
  fail(conn,q.ID,"UNSUPPORTED_ACTION");return
 }
 if opErr!=nil {fail(conn,q.ID,classify(opErr))}
}

func read(parent context.Context,c *websocket.Conn)(websocket.MessageType,[]byte,error){
 ctx,cancel:=context.WithTimeout(parent,40*time.Second);defer cancel()
 return c.Read(ctx)
}
func write(parent context.Context,c *websocket.Conn,v any)error{
 b,err:=json.Marshal(v);if err!=nil{return err}
 ctx,cancel:=context.WithTimeout(parent,25*time.Second);defer cancel()
 return c.Write(ctx,websocket.MessageText,b)
}
func binary(parent context.Context,c *websocket.Conn,b []byte)error{
 ctx,cancel:=context.WithTimeout(parent,25*time.Second);defer cancel()
 return c.Write(ctx,websocket.MessageBinary,b)
}
func fail(c *websocket.Conn,id,code string){
 ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second);defer cancel()
 _=write(ctx,c,map[string]any{"v":1,"type":"file.error","id":id,"code":code})
}
func classify(err error)string{
 switch {
 case errors.Is(err,errChecksumMismatch):return "CHECKSUM_MISMATCH"
 case os.IsExist(err):return "TARGET_EXISTS"
 case os.IsNotExist(err):return "NOT_FOUND"
 case os.IsPermission(err):return "PERMISSION_DENIED"
 case errors.Is(err,io.ErrUnexpectedEOF),errors.Is(err,io.EOF):return "TRANSFER_INTERRUPTED"
 default:return "FILE_OPERATION_FAILED"
 }
}
func(s *Service)download(ctx context.Context,c *websocket.Conn,root *os.Root,q Request)error{
 f,err:=root.Open(q.Path);if err!=nil{return err}
 defer f.Close()
 st,err:=f.Stat();if err!=nil{return err}
 if !st.Mode().IsRegular(){return errors.New("not regular")}
 if err:=write(ctx,c,map[string]any{"v":1,"id":q.ID,"type":"file.ready","size_bytes":st.Size()});err!=nil{return err}
 h:=sha256.New()
 buf:=make([]byte,ChunkBytes)
 var count int64
 for {
  if err:=ctx.Err();err!=nil{return err}
  n,e:=f.Read(buf)
  if n>0 {
   h.Write(buf[:n]);count+=int64(n)
   if err:=binary(ctx,c,buf[:n]);err!=nil{return err}
  }
  if e==io.EOF{break}
  if e!=nil{return e}
 }
 if count!=st.Size(){return errChecksumMismatch}
 return write(ctx,c,map[string]any{"v":1,"type":"file.complete","id":q.ID,"size_bytes":count,"sha256":hex.EncodeToString(h.Sum(nil))})
}
func(s *Service)upload(ctx context.Context,c *websocket.Conn,root *os.Root,q Request)error{
 max:=s.MaxUpload
 if max<=0 {max=DefaultMaxUpload}
 if q.SizeBytes<0||q.SizeBytes>max{return errors.New("UPLOAD_LIMIT_EXCEEDED")}
 dir,base:=path.Split(q.Path)
 if base=="" {return errors.New("INVALID_PATH")}
 tmpName,err:=randomName();if err!=nil{return err}
 tmp:=dir+tmpName
 f,err:=root.OpenFile(tmp,os.O_WRONLY|os.O_CREATE|os.O_EXCL,0600)
 if err!=nil{return err}
 defer root.Remove(tmp)
 defer f.Close()
 if err:=write(ctx,c,map[string]any{"v":1,"type":"file.ready","id":q.ID,"size_bytes":q.SizeBytes});err!=nil{return err}
 h:=sha256.New()
 var written int64
 for {
  typ,payload,e:=read(ctx,c)
  if e!=nil{return e}
  if typ==websocket.MessageBinary {
   if len(payload)>ChunkBytes || written+int64(len(payload))>q.SizeBytes {return errors.New("INVALID_UPLOAD_SIZE")}
   if _,e=f.Write(payload);e!=nil{return e}
   h.Write(payload);written+=int64(len(payload))
   continue
  }
  if typ!=websocket.MessageText{return errors.New("INVALID_TRANSFER_FRAME")}
  var end struct{V int `json:"v"`;ID string `json:"id"`;Type string `json:"type"`;SHA256 string `json:"sha256"`}
  if json.Unmarshal(payload,&end)!=nil||end.V!=1||end.ID!=q.ID||end.Type!="file.end" {return errors.New("INVALID_END")}
  digest:=hex.EncodeToString(h.Sum(nil))
  if written!=q.SizeBytes||end.SHA256!=digest{return errChecksumMismatch}
  // Keep the temporary file private during upload. Publish it as world
  // readable/writable only after checksum verification, before hard-linking.
  // Explicit Chmod is required because the process umask masks OpenFile modes.
  if err:=f.Chmod(0o666);err!=nil{return err}
  if err:=f.Sync();err!=nil{return err}
  if err:=f.Close();err!=nil{return err}
  // Link creates destination ONLY if absent. Never silently overwrite files.
  if err:=root.Link(tmp,q.Path);err!=nil{return err}
  if err:=root.Remove(tmp);err!=nil{return err}
  return write(ctx,c,map[string]any{"v":1,"type":"file.complete","id":q.ID,"size_bytes":written,"sha256":digest})
 }
}
// mkdir creates the directory privately, then makes it traversable/writable
// for local users. An explicit chmod defeats restrictive process umasks.
// Root.OpenRoot pins the created directory before its permissions are changed.
func (s *Service) mkdir(root *os.Root, name string) error {
 if err := root.Mkdir(name, 0o700); err != nil {return err}
 child, err := root.OpenRoot(name)
 if err != nil {
  _ = root.Remove(name)
  return err
 }
 err = child.Chmod(".", 0o777)
 closeErr := child.Close()
 if err == nil {err=closeErr}
 if err != nil {_ = root.Remove(name)}
 return err
}

func(s *Service)delete(root *os.Root,p string)error{
 st,err:=root.Lstat(p);if err!=nil{return err}
 if !st.Mode().IsRegular() && !st.IsDir(){return errors.New("UNSAFE_ENTRY_TYPE")}
 // Only empty folders are removable; recursive deletion is intentionally absent.
 return root.Remove(p)
}
// rename performs a no-overwrite, root-confined move of a regular file.
// Link+unlink preserves file data and modes without downloading it to UI.
// It works inside one filesystem; EXDEV and other filesystem errors propagate.
func(s *Service)rename(root *os.Root,old,new string)error{
 st,err:=root.Lstat(old);if err!=nil{return err}
 if !st.Mode().IsRegular(){return errors.New("ONLY_REGULAR_FILES_SUPPORTED")}
 if err:=root.Link(old,new);err!=nil{return err}
 if err:=root.Remove(old);err!=nil {
  _=root.Remove(new)
  return err
 }
 return nil
}
