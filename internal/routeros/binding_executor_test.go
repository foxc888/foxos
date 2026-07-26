package routeros

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/confirmation"
)
type recordingWriter struct{calls int}
func(w *recordingWriter)Apply(context.Context,Operation)error{w.calls++;return nil}

func TestBindingExecutorRequiresUntamperedConfirmation(t *testing.T){
	signer,err:=confirmation.New([]byte("01234567890123456789012345678901"));if err!=nil{t.Fatal(err)}
	writer:=&recordingWriter{};executor,err:=NewBindingExecutor(writer,signer);if err!=nil{t.Fatal(err)}
	plan:=Plan{PolicyID:"phone",RequiresConfirmation:true,Operations:[]Operation{{Method:http.MethodPatch,Path:"/rest/ip/dhcp-server/lease/*1",Body:map[string]string{"comment":"foxos:device:phone"},"Summary":"bind",OwnedComment:"foxos:device:phone"}}}
	token,err:=signer.Issue(plan,5*time.Minute);if err!=nil{t.Fatal(err)}
	tampered:=plan;tampered.Operations=append([]Operation(nil),plan.Operations...);tampered.Operations[0].Path="/rest/system/reboot"
	if err:=executor.Execute(context.Background(),tampered,token);!errors.Is(err,confirmation.ErrPlanChanged){t.Fatalf("err=%v",err)}
	if writer.calls!=0{t.Fatalf("calls=%d",writer.calls)}
	if err:=executor.Execute(context.Background(),plan,token);err!=nil{t.Fatal(err)}
	if writer.calls!=1{t.Fatalf("calls=%d",writer.calls)}
}
func TestBindingExecutorRejectsUnownedOperation(t *testing.T){
	signer,_:=confirmation.New([]byte("01234567890123456789012345678901"));writer:=&recordingWriter{};executor,_:=NewBindingExecutor(writer,signer)
	plan:=Plan{RequiresConfirmation:true,Operations:[]Operation{{Method:http.MethodPatch,Path:"/rest/ip/dhcp-server/lease/*1",Body:map[string]string{"comment":"manual"},"OwnedComment":"manual"}}}
	token,_:=signer.Issue(plan,time.Minute)
	if err:=executor.Execute(context.Background(),plan,token);!errors.Is(err,ErrUnsafeOperation){t.Fatalf("err=%v",err)}
	if writer.calls!=0{t.Fatalf("calls=%d",writer.calls)}
}
