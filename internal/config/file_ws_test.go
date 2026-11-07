package config

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestFileWSPrivateLANWithoutStaticClientIP(t *testing.T) {
    cases:=[]struct {
        name string
        mode string
        allowed string
        wantErr string
    }{
        {"trusted_lan", "trusted_network", "[]", ""},
        {"trusted_restricted", "trusted_network", "[\"192.168.0.12\"]", ""},
        {"token_missing_acl", "token", "[]", "FILE_WS_ALLOWED_CLIENT_IPS_REQUIRED"},
        {"legacy_missing_acl", "", "[]", "FILE_WS_ALLOWED_CLIENT_IPS_REQUIRED"},
        {"trusted_invalid_peer", "trusted_network", "[\"8.8.8.8\"]", "FILE_WS_INVALID_CLIENT_IP"},
    }
    for _,tc:=range cases {
        t.Run(tc.name,func(t *testing.T){
            path:=filepath.Join(t.TempDir(),"config.json")
            data:=`{"config_version":1,"file_ws":{"enabled":true,"listen":"192.168.0.147:8093","auth_mode":"`+tc.mode+`","allowed_client_ips":`+tc.allowed+`}}`
            if err:=os.WriteFile(path,[]byte(data),0600);err!=nil{t.Fatal(err)}
            cfg,err:=Load(path)
            if tc.wantErr=="" {
                if err!=nil{t.Fatal(err)}
                if cfg.FileWS.AuthMode!="trusted_network"{t.Fatalf("mode: %q",cfg.FileWS.AuthMode)}
            } else if err==nil || !strings.Contains(err.Error(),tc.wantErr) {
                t.Fatalf("got %v; want %s",err,tc.wantErr)
            }
        })
    }
}
