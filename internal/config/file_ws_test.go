package config

import (
 "os"
 "path/filepath"
 "testing"
)

func TestFileTransferWorksWithoutFileWSConfiguration(t *testing.T) {
 for name, payload := range map[string]string{
  "new_config": `{"config_version":1}`,
  "legacy_disabled": `{"config_version":1,"file_ws":{"enabled":false,"listen":"","auth_mode":"token","allowed_client_ips":[]}}`,
  "legacy_token": `{"config_version":1,"file_ws":{"enabled":true,"listen":"192.168.0.147:8093","auth_mode":"token","token_file":"/root/old-filews-token"}}`,
 } {
  t.Run(name,func(t *testing.T){
   filename:=filepath.Join(t.TempDir(),"config.json")
   if err:=os.WriteFile(filename,[]byte(payload),0600);err!=nil{t.Fatal(err)}
   if _,err:=Load(filename);err!=nil{t.Fatalf("obsolete file_ws settings must not gate startup: %v",err)}
  })
 }
}
