package filews

import (
 "context"
 "errors"
 "crypto/rand"
 "encoding/hex"
 "io"
 "os"
 "path"
 "strings"
)

// File and folder mutations always stay inside the pinned, administrator-
// approved os.Root. The user cannot address the internal staging path.
const (
 maxCopyEntries = 100_000
 maxCopyBytes = int64(1) << 40 // 1 TiB, avoids accidentally copying an unbounded tree.
)
var errCopyLimit = errors.New("COPY_LIMIT_EXCEEDED")
var errUnsafeEntry = errors.New("UNSAFE_ENTRY_TYPE")
var errSelfDestination = errors.New("INVALID_PATH")

func randomCopyName()(string,error){var b [16]byte; if _,err:=rand.Read(b[:]);err!=nil{return "",err};return ".hcss-copy-"+hex.EncodeToString(b[:]),nil}

func (s *Service) touch(root *os.Root, name string) error {
 f,err:=root.OpenFile(name,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
 if err!=nil{return err}
 if err=f.Chmod(0o666);err!=nil{_ = f.Close();_ = root.Remove(name);return err}
 return f.Close()
}

func checkDestination(root *os.Root, new string) error {
 _,err:=root.Lstat(new)
 if err==nil{return os.ErrExist}
 if !os.IsNotExist(err){return err}
 return nil
}

func (s *Service) moveEntry(root *os.Root,old,new string) error {
 if strings.HasPrefix(new,old+"/"){return errSelfDestination}
 st,err:=root.Lstat(old)
 if err!=nil{return err}
 if st.Mode().IsRegular(){return s.rename(root,old,new)}
 if !st.IsDir(){return errUnsafeEntry}
 // Within one root, directory rename is atomic on the filesystem. The
 // destination must not exist; the service serializes its own mutations.
 // An unrelated local writer is outside this process' coordination.
 if err:=checkDestination(root,new);err!=nil{return err}
 return root.Rename(old,new)
}

type copyBudget struct {entries int; bytes int64}
func (b *copyBudget) add(size int64)error {
 b.entries++
 if b.entries>maxCopyEntries||size<0||size>maxCopyBytes-b.bytes{return errCopyLimit}
 b.bytes+=size
 return nil
}

// copyEntry performs a deep, independent filesystem copy, not a hard link.
// Data never passes through Backend or HomeCloudUI. It first creates a
// private random temporary entry at the destination and only publishes a
// complete copy; failures/cancellation remove that staging tree.
func (s *Service) copyEntry(ctx context.Context, root *os.Root, old,new string)error{
 if strings.HasPrefix(new,old+"/"){return errSelfDestination}
 if err:=ctx.Err();err!=nil{return err}
 src,err:=root.Lstat(old)
 if err!=nil{return err}
 if !src.Mode().IsRegular() && !src.IsDir(){return errUnsafeEntry}
 if err=checkDestination(root,new);err!=nil{return err}
 tmp,err:=randomCopyName()
 if err!=nil{return err}
 stage:=path.Join(path.Dir(new),tmp)
 if path.Dir(new)=="." {stage=tmp}
 // os.Root methods reject symlinks escaping the configured root.
 // Every child is Lstat'd before use, and symlink entries are rejected.
 budget:=&copyBudget{}
 if err=s.copyRecursive(ctx,root,old,stage,budget);err!=nil{
  _=root.RemoveAll(stage)
  return err
 }
 defer root.RemoveAll(stage)
 if err=ctx.Err();err!=nil{return err}
 if err=checkDestination(root,new);err!=nil{return err}
 if src.IsDir(){
  return root.Rename(stage,new)
 }
 // Link is atomic and no-clobber for files even if another process creates
 // destination in the meantime. Unlinking stage leaves the independent copy.
 if err=root.Link(stage,new);err!=nil{return err}
 return root.Remove(stage)
}

func (s *Service) copyRecursive(ctx context.Context,root *os.Root,old,new string,b *copyBudget)error{
 if err:=ctx.Err();err!=nil{return err}
 st,err:=root.Lstat(old)
 if err!=nil{return err}
 if err=b.add(st.Size());err!=nil{return err}
 if st.IsDir(){
  if err:=root.Mkdir(new,0700);err!=nil{return err}
  f,err:=root.Open(old)
  if err!=nil{return err}
  entries,err:=f.ReadDir(-1)
  closeErr:=f.Close()
  if err!=nil{return err}
  if closeErr!=nil{return closeErr}
  for _,entry:=range entries{
   if err=ctx.Err();err!=nil{return err}
   // Block special objects, symlinks and nested staging artifacts.
   if entry.Type()&os.ModeType!=0 && !entry.IsDir(){return errUnsafeEntry}
   if strings.HasPrefix(entry.Name(),".hcss-upload-")||strings.HasPrefix(entry.Name(),".hcss-copy-"){
    return errUnsafeEntry
   }
   if err:=s.copyRecursive(ctx,root,path.Join(old,entry.Name()),path.Join(new,entry.Name()),b);err!=nil{return err}
  }
  d,err:=root.OpenRoot(new);if err!=nil{return err}
  err=d.Chmod(".",0777)
  closeErr=d.Close()
  if err==nil{err=closeErr}
  return err
 }
 if !st.Mode().IsRegular(){return errUnsafeEntry}
 in,err:=root.Open(old)
 if err!=nil{return err}
 defer in.Close()
 out,err:=root.OpenFile(new,os.O_WRONLY|os.O_CREATE|os.O_EXCL,0600)
 if err!=nil{return err}
 buf:=make([]byte,ChunkBytes)
 n,copyErr:=copyContext(ctx,out,in,buf)
 if copyErr==nil&&n!=st.Size(){copyErr=io.ErrUnexpectedEOF}
 if copyErr==nil{copyErr=out.Chmod(0666)}
 if copyErr==nil{copyErr=out.Sync()}
 closeErr:=out.Close()
 if copyErr!=nil{return copyErr}
 return closeErr
}

func copyContext(ctx context.Context,w io.Writer,r io.Reader,buf []byte)(int64,error){
 var total int64
 for {
  if err:=ctx.Err();err!=nil{return total,err}
  n,err:=r.Read(buf)
  if n>0{
   used,e:=w.Write(buf[:n])
   total+=int64(used)
   if e!=nil{return total,e}
   if used!=n{return total,io.ErrShortWrite}
  }
  if err==io.EOF{return total,nil}
  if err!=nil{return total,err}
 }
}
