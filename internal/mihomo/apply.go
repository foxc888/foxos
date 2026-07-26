package mihomo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

var (
	ErrApplyFailed = errors.New("mihomo config apply failed")
	ErrRollbackFailed = errors.New("mihomo config rollback failed")
)

type Runtime interface {
	Validate(context.Context,string) error
	Reload(context.Context) error
	Healthy(context.Context) error
}

type ApplyResult struct {
	BackupPath string
	AppliedAt time.Time
	RolledBack bool
}

type Applier struct {
	ConfigPath string
	BackupDir string
	Runtime Runtime
	Now func() time.Time
}

func (a Applier) Apply(ctx context.Context,body []byte)(ApplyResult,error){
	if ctx==nil||a.Runtime==nil||len(body)==0{return ApplyResult{},fmt.Errorf("%w: invalid input",ErrApplyFailed)}
	configPath,err:=filepath.Abs(a.ConfigPath);if err!=nil{return ApplyResult{},err}
	backupDir,err:=filepath.Abs(a.BackupDir);if err!=nil{return ApplyResult{},err}
	if configPath==backupDir||filepath.Dir(configPath)==configPath{return ApplyResult{},fmt.Errorf("%w: unsafe path",ErrApplyFailed)}
	if err:=os.MkdirAll(filepath.Dir(configPath),0o700);err!=nil{return ApplyResult{},err}
	if err:=os.MkdirAll(backupDir,0o700);err!=nil{return ApplyResult{},err}
	temp,err:=os.CreateTemp(filepath.Dir(configPath),".foxos-config-*.yaml");if err!=nil{return ApplyResult{},err}
	tempPath:=temp.Name()
	defer os.Remove(tempPath)
	if err:=temp.Chmod(0o600);err!=nil{temp.Close();return ApplyResult{},err}
	if _,err:=temp.Write(body);err!=nil{temp.Close();return ApplyResult{},err}
	if err:=temp.Sync();err!=nil{temp.Close();return ApplyResult{},err}
	if err:=temp.Close();err!=nil{return ApplyResult{},err}
	if err:=a.Runtime.Validate(ctx,tempPath);err!=nil{return ApplyResult{},fmt.Errorf("%w: validate: %v",ErrApplyFailed,err)}

	now:=time.Now().UTC();if a.Now!=nil{now=a.Now().UTC()}
	result:=ApplyResult{AppliedAt:now}
	if _,err:=os.Stat(configPath);err==nil{
		result.BackupPath=filepath.Join(backupDir,"config-"+now.Format("20060102T150405.000000000Z")+".yaml")
		if err:=copyFile(configPath,result.BackupPath);err!=nil{return ApplyResult{},fmt.Errorf("%w: snapshot: %v",ErrApplyFailed,err)}
	}else if !errors.Is(err,os.ErrNotExist){return ApplyResult{},err}
	if err:=os.Rename(tempPath,configPath);err!=nil{return ApplyResult{},fmt.Errorf("%w: replace: %v",ErrApplyFailed,err)}
	if err:=a.Runtime.Reload(ctx);err!=nil{return a.rollback(ctx,result,configPath,err)}
	if err:=a.Runtime.Healthy(ctx);err!=nil{return a.rollback(ctx,result,configPath,err)}
	return result,nil
}

func (a Applier) rollback(ctx context.Context,result ApplyResult,configPath string,cause error)(ApplyResult,error){
	result.RolledBack=true
	if result.BackupPath==""{return result,fmt.Errorf("%w: %v; no previous config",ErrRollbackFailed,cause)}
	if err:=copyFile(result.BackupPath,configPath);err!=nil{return result,fmt.Errorf("%w: restore: %v",ErrRollbackFailed,err)}
	if err:=a.Runtime.Reload(ctx);err!=nil{return result,fmt.Errorf("%w: reload restored config: %v",ErrRollbackFailed,err)}
	return result,fmt.Errorf("%w: runtime check: %v",ErrApplyFailed,cause)
}

func copyFile(source,destination string)error{
	input,err:=os.Open(source);if err!=nil{return err};defer input.Close()
	output,err:=os.OpenFile(destination,os.O_CREATE|os.O_WRONLY|os.O_TRUNC,0o600);if err!=nil{return err}
	if _,err:=io.Copy(output,input);err!=nil{output.Close();return err}
	if err:=output.Sync();err!=nil{output.Close();return err}
	return output.Close()
}
