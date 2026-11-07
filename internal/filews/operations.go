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
 "time"
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
func (s *Service) copyEntry(ctx context.Context, root *os.Root, old,new string,notify func(int64)error)error{
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
 progress:=&copyProgress{notify:notify,last:time.Now(),next:4<<20}
 if err=s.copyRecursive(ctx,root,old,stage,budget,progress);err!=nil{
  _=root.RemoveAll(stage)
  return err
 }
 defer root.RemoveAll(stage)
 if err=ctx.Err();err!=nil{return err}
 // Only publishing is serialized with mkdir/move/delete/rename. Staging
 // stays private, so another mutation can complete during a long copy.
 s.operations.Lock()
 defer s.operations.Unlock()
 if err=checkDestination(root,new);err!=nil{return err}
 if src.IsDir(){
  return root.Rename(stage,new)
 }
 // Link is atomic and no-clobber for files even if another process creates
 // destination in the meantime. Unlinking stage leaves the independent copy.
 if err=root.Link(stage,new);err!=nil{return err}
 return root.Remove(stage)
}

// Wire-level progress is advisory; the copy stays inside HC SS.
type copyProgress struct { copied int64; next int64; last time.Time; notify func(int64)error }
func (p *copyProgress) advance(n int64)error{
 if p==nil{return nil}
 p.copied+=n
 if p.notify==nil{return nil}
 if p.copied<p.next&&time.Since(p.last)<3*time.Second{return nil}
 if err:=p.notify(p.copied);err!=nil{return err}
 p.last=time.Now();p.next=p.copied+(4<<20)
 return nil
}

func (s *Service) copyRecursive(ctx context.Context,root *os.Root,old,new string,b *copyBudget,p *copyProgress)error{
 if err:=ctx.Err();err!=nil{return err}
 st,err:=root.Lstat(old)
 if err!=nil{return err}
 if err=b.add(st.Size());err!=nil{return err}
 if st.IsDir(){
  if err:=root.Mkdir(new,0700);err!=nil{return err}
  f,err:=root.Open(old)
  if err!=nil{return err}
  // Never use ReadDir(-1): a hostile or very large directory must not
  // allocate its entire listing before the entry limit is applied.
  for {
   entries,readErr:=f.ReadDir(128)
   for _,entry:=range entries{
    if err=ctx.Err();err!=nil{_ = f.Close();return err}
    // Reject links/special files and private staging artifacts.
    if entry.Type()&os.ModeType!=0 && !entry.IsDir(){_ = f.Close();return errUnsafeEntry}
    if strings.HasPrefix(entry.Name(),".hcss-upload-")||strings.HasPrefix(entry.Name(),".hcss-copy-"){
     _ = f.Close();return errUnsafeEntry
    }
    if err=s.copyRecursive(ctx,root,path.Join(old,entry.Name()),path.Join(new,entry.Name()),b,p);err!=nil{_ = f.Close();return err}
    if err=p.advance(0);err!=nil{_ = f.Close();return err}
   }
   if readErr==io.EOF{break}
   if readErr!=nil{_ = f.Close();return readErr}
  }
  if err=f.Close();err!=nil{return err}
  d,err:=root.OpenRoot(new);if err!=nil{return err}
  err=d.Chmod(".",0777)
  closeErr:=d.Close()
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
 n,copyErr:=copyContext(ctx,out,in,buf,p.advance)
 if copyErr==nil&&n!=st.Size(){copyErr=io.ErrUnexpectedEOF}
 if copyErr==nil{copyErr=out.Chmod(0666)}
 if copyErr==nil{copyErr=out.Sync()}
 closeErr:=out.Close()
 if copyErr!=nil{return copyErr}
 return closeErr
}

func copyContext(ctx context.Context,w io.Writer,r io.Reader,buf []byte,progress func(int64)error)(int64,error){
 var total int64
 for {
  if err:=ctx.Err();err!=nil{return total,err}
  n,err:=r.Read(buf)
  if n>0{
   used,e:=w.Write(buf[:n])
   total+=int64(used)
   if e!=nil{return total,e}
   if used!=n{return total,io.ErrShortWrite}
   if progress!=nil {if e:=progress(int64(used));e!=nil{return total,e}}
  }
  if err==io.EOF{return total,nil}
  if err!=nil{return total,err}
 }
}
