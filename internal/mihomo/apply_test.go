package mihomo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeRuntime struct{validateErr,reloadErr,healthErr error;reloads int}
func(f *fakeRuntime)Validate(context.Context,string)error{return f.validateErr}
func(f *fakeRuntime)Reload(context.Context)error{f.reloads++;if f.reloads==1{return f.reloadErr};return nil}
func(f *fakeRuntime)Healthy(context.Context)error{return f.healthErr}

func TestApplySuccess(t *testing.T){
	root:=t.TempDir();config:=filepath.Join(root,"config.yaml")
	if err:=os.WriteFile(config,[]byte("old"),0o600);err!=nil{t.Fatal(err)}
	runtime:=&fakeRuntime{}
	applier:=Applier{ConfigPath:config,BackupDir:filepath.Join(root,"backups"),Runtime:runtime,Now:func()time.Time{return time.Date(2026,7,26,1,2,3,0,time.UTC)}}
	result,err:=applier.Apply(context.Background(),[]byte("new"))
	if err!=nil{t.Fatal(err)}
	if result.BackupPath==""||result.RolledBack{t.Fatalf("result=%+v",result)}
	body,_:=os.ReadFile(config);if string(body)!="new"{t.Fatalf("config=%q",body)}
	backup,_:=os.ReadFile(result.BackupPath);if string(backup)!="old"{t.Fatalf("backup=%q",backup)}
}

func TestApplyRollsBackOnHealthFailure(t *testing.T){
	root:=t.TempDir();config:=filepath.Join(root,"config.yaml");_ = os.WriteFile(config,[]byte("old"),0o600)
	runtime:=&fakeRuntime{healthErr:errors.New("unhealthy")}
	result,err:= (Applier{ConfigPath:config,BackupDir:filepath.Join(root,"backups"),Runtime:runtime}).Apply(context.Background(),[]byte("new"))
	if !errors.Is(err,ErrApplyFailed)||!result.RolledBack{t.Fatalf("result=%+v err=%v",result,err)}
	body,_:=os.ReadFile(config);if string(body)!="old"{t.Fatalf("rollback config=%q",body)}
	if runtime.reloads!=2{t.Fatalf("reloads=%d",runtime.reloads)}
}

func TestApplyStopsBeforeWriteOnValidationFailure(t *testing.T){
	root:=t.TempDir();config:=filepath.Join(root,"config.yaml");_ = os.WriteFile(config,[]byte("old"),0o600)
	_,err:=(Applier{ConfigPath:config,BackupDir:filepath.Join(root,"backups"),Runtime:&fakeRuntime{validateErr:errors.New("bad")}}).Apply(context.Background(),[]byte("new"))
	if !errors.Is(err,ErrApplyFailed){t.Fatalf("err=%v",err)}
	body,_:=os.ReadFile(config);if string(body)!="old"{t.Fatalf("config changed=%q",body)}
}
