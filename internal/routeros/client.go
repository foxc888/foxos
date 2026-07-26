package routeros

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct{
	base *url.URL
	username string
	password string
	http *http.Client
}

func NewClient(endpoint,username,password string)(*Client,error){
	base,err:=url.Parse(endpoint)
	if err!=nil||base.Host==""||(base.Scheme!="http"&&base.Scheme!="https")||base.User!=nil{return nil,errors.New("invalid RouterOS REST endpoint")}
	if username==""||password==""{return nil,errors.New("RouterOS credentials are required")}
	return &Client{base:base,username:username,password:password,http:&http.Client{Timeout:10*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}},nil
}

type Resource struct{
	Version string `json:"version"`
	Architecture string `json:"architecture-name"`
	BoardName string `json:"board-name"`
	CPU string `json:"cpu"`
	CPUCount string `json:"cpu-count"`
	CPULoad string `json:"cpu-load"`
	FreeMemory string `json:"free-memory"`
	TotalMemory string `json:"total-memory"`
	FreeHDD string `json:"free-hdd-space"`
	TotalHDD string `json:"total-hdd-space"`
	Uptime string `json:"uptime"`
}

type Interface struct{
	ID string `json:".id"`
	Name string `json:"name"`
	Type string `json:"type"`
	MACAddress string `json:"mac-address"`
	Running string `json:"running"`
	Disabled string `json:"disabled"`
	RXByte string `json:"rx-byte"`
	TXByte string `json:"tx-byte"`
}

type Lease struct{
	ID string `json:".id"`
	Address string `json:"address"`
	MACAddress string `json:"mac-address"`
	HostName string `json:"host-name"`
	Status string `json:"status"`
	Dynamic string `json:"dynamic"`
	Server string `json:"server"`
	LastSeen string `json:"last-seen"`
}

type ARP struct{
	ID string `json:".id"`
	Address string `json:"address"`
	MACAddress string `json:"mac-address"`
	Interface string `json:"interface"`
	Complete string `json:"complete"`
}

func(c *Client)Resource(ctx context.Context)(Resource,error){var out Resource;err:=c.get(ctx,"/rest/system/resource",&out);return out,err}
func(c *Client)Interfaces(ctx context.Context)([]Interface,error){var out []Interface;err:=c.get(ctx,"/rest/interface",&out);return out,err}
func(c *Client)Leases(ctx context.Context)([]Lease,error){var out []Lease;err:=c.get(ctx,"/rest/ip/dhcp-server/lease",&out);return out,err}
func(c *Client)ARP(ctx context.Context)([]ARP,error){var out []ARP;err:=c.get(ctx,"/rest/ip/arp",&out);return out,err}

func(c *Client)get(ctx context.Context,path string,destination any)error{
	if !allowedReadPath(path){return errors.New("RouterOS path is not allowed")}
	target:=*c.base;target.Path=strings.TrimRight(c.base.Path,"/")+path;target.RawQuery=""
	request,err:=http.NewRequestWithContext(ctx,http.MethodGet,target.String(),nil);if err!=nil{return err}
	request.SetBasicAuth(c.username,c.password);request.Header.Set("Accept","application/json")
	response,err:=c.http.Do(request);if err!=nil{return err};defer response.Body.Close()
	if response.StatusCode==http.StatusUnauthorized||response.StatusCode==http.StatusForbidden{return errors.New("RouterOS authentication failed")}
	if response.StatusCode!=http.StatusOK{body,_:=io.ReadAll(io.LimitReader(response.Body,4096));return fmt.Errorf("RouterOS status %d: %s",response.StatusCode,strings.TrimSpace(string(body)))}
	decoder:=json.NewDecoder(io.LimitReader(response.Body,8<<20))
	if err:=decoder.Decode(destination);err!=nil{return fmt.Errorf("decode RouterOS response: %w",err)}
	return nil
}
func allowedReadPath(path string)bool{switch path{case "/rest/system/resource","/rest/interface","/rest/ip/dhcp-server/lease","/rest/ip/arp":return true};return false}
