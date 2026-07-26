package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Controller struct {
	base *url.URL
	secret string
	reloadPath string
	http *http.Client
}

func NewController(endpoint,secret,reloadPath string)(*Controller,error){
	base,err:=url.Parse(endpoint)
	if err!=nil||base.Host==""||(base.Scheme!="http"&&base.Scheme!="https")||base.User!=nil{return nil,errors.New("invalid Mihomo controller endpoint")}
	if strings.TrimSpace(secret)!=secret||len(secret)>4096{return nil,errors.New("invalid Mihomo secret")}
	if reloadPath==""{return nil,errors.New("Mihomo config path is required")}
	return &Controller{base:base,secret:secret,reloadPath:reloadPath,http:&http.Client{Timeout:10*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}},nil
}

func (c *Controller) Validate(ctx context.Context,path string)error{
	body,err:=os.ReadFile(path);if err!=nil{return err}
	if len(body)==0||len(body)>16<<20{return errors.New("invalid config size")}
	var document yaml.Node
	if err:=yaml.Unmarshal(body,&document);err!=nil{return fmt.Errorf("yaml: %w",err)}
	if len(document.Content)==0||document.Content[0].Kind!=yaml.MappingNode{return errors.New("config root must be a mapping")}
	return nil
}

func (c *Controller) Reload(ctx context.Context)error{
	body,err:=json.Marshal(map[string]string{"path":c.reloadPath});if err!=nil{return err}
	response,err:=c.do(ctx,http.MethodPut,"/configs?force=true",bytes.NewReader(body),"application/json")
	if err!=nil{return err}
	defer response.Body.Close()
	if response.StatusCode!=http.StatusNoContent&&response.StatusCode!=http.StatusOK{return statusError(response)}
	return nil
}

func (c *Controller) Healthy(ctx context.Context)error{
	response,err:=c.do(ctx,http.MethodGet,"/version",nil,"")
	if err!=nil{return err}
	defer response.Body.Close()
	if response.StatusCode!=http.StatusOK{return statusError(response)}
	var value struct{Version string `json:"version"`}
	decoder:=json.NewDecoder(io.LimitReader(response.Body,1<<20))
	if err:=decoder.Decode(&value);err!=nil||value.Version==""{return errors.New("invalid Mihomo version response")}
	return nil
}

func (c *Controller) do(ctx context.Context,method,path string,body io.Reader,contentType string)(*http.Response,error){
	target:=*c.base;target.Path=strings.TrimRight(c.base.Path,"/")+strings.Split(path,"?")[0]
	if index:=strings.Index(path,"?");index>=0{target.RawQuery=path[index+1:]}
	request,err:=http.NewRequestWithContext(ctx,method,target.String(),body);if err!=nil{return nil,err}
	request.Header.Set("Accept","application/json")
	if contentType!=""{request.Header.Set("Content-Type",contentType)}
	if c.secret!=""{request.Header.Set("Authorization","Bearer "+c.secret)}
	return c.http.Do(request)
}
func statusError(response *http.Response)error{
	body,_:=io.ReadAll(io.LimitReader(response.Body,4096))
	return fmt.Errorf("Mihomo controller status %d: %s",response.StatusCode,strings.TrimSpace(string(body)))
}
