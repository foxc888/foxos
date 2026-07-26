package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
)

type NodeStore interface {
	SaveNode(context.Context, domain.Node) error
	Node(context.Context, string) (domain.Node, error)
	Nodes(context.Context) ([]domain.Node, error)
	DeleteNode(context.Context, string) error
}

type Server struct {
	nodes NodeStore
	token string
}

func New(nodes NodeStore, token string) (*Server, error) {
	if nodes == nil || len(token) < 32 { return nil, errors.New("node store and API token of at least 32 characters are required") }
	return &Server{nodes:nodes,token:token},nil
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/nodes",s.auth(http.HandlerFunc(s.listNodes)))
	mux.Handle("POST /api/v1/nodes",s.auth(http.HandlerFunc(s.createNode)))
	mux.Handle("GET /api/v1/nodes/{id}",s.auth(http.HandlerFunc(s.getNode)))
	mux.Handle("PUT /api/v1/nodes/{id}",s.auth(http.HandlerFunc(s.updateNode)))
	mux.Handle("DELETE /api/v1/nodes/{id}",s.auth(http.HandlerFunc(s.deleteNode)))
}

type nodeInput struct {
	ID string `json:"id,omitempty"`
	Name string `json:"name"`
	Type string `json:"type"`
	Server string `json:"server"`
	Port int `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	UUID string `json:"uuid,omitempty"`
	Cipher string `json:"cipher,omitempty"`
	Network string `json:"network,omitempty"`
	SNI string `json:"sni,omitempty"`
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
	UDP bool `json:"udp,omitempty"`
	TLS bool `json:"tls,omitempty"`
	SkipCertVerify bool `json:"skipCertVerify,omitempty"`
	Extra map[string]any `json:"extra,omitempty"`
}
type nodeOutput struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Server string `json:"server"`
	Port int `json:"port"`
	Network string `json:"network,omitempty"`
	SNI string `json:"sni,omitempty"`
	UDP bool `json:"udp,omitempty"`
	TLS bool `json:"tls,omitempty"`
	HasCredential bool `json:"hasCredential"`
}
func (p nodeInput) domain() domain.Node { return domain.Node{ID:p.ID,Name:p.Name,Type:strings.ToLower(p.Type),Server:p.Server,Port:p.Port,Username:p.Username,Password:p.Password,UUID:p.UUID,Cipher:p.Cipher,Network:p.Network,SNI:p.SNI,Path:p.Path,Host:p.Host,UDP:p.UDP,TLS:p.TLS,SkipCertVerify:p.SkipCertVerify,Extra:p.Extra} }
func output(n domain.Node) nodeOutput { return nodeOutput{ID:n.ID,Name:n.Name,Type:n.Type,Server:n.Server,Port:n.Port,Network:n.Network,SNI:n.SNI,UDP:n.UDP,TLS:n.TLS,HasCredential:n.Password!=""||n.UUID!=""} }

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		const prefix="Bearer "
		value:=r.Header.Get("Authorization")
		if !strings.HasPrefix(value,prefix)||subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(value,prefix)),[]byte(s.token))!=1 {
			w.Header().Set("WWW-Authenticate","Bearer")
			problem(w,http.StatusUnauthorized,"unauthorized",errors.New("valid bearer token required"))
			return
		}
		next.ServeHTTP(w,r)
	})
}
func (s *Server) listNodes(w http.ResponseWriter,r *http.Request){nodes,err:=s.nodes.Nodes(r.Context());if err!=nil{problem(w,500,"list_failed",err);return};out:=make([]nodeOutput,0,len(nodes));for _,n:=range nodes{out=append(out,output(n))};writeJSON(w,200,out)}
func (s *Server) createNode(w http.ResponseWriter,r *http.Request){var in nodeInput;if err:=decode(r,&in);err!=nil{problem(w,400,"invalid_json",err);return};if in.ID==""{in.ID=randomID()};node:=in.domain();if err:=s.nodes.SaveNode(r.Context(),node);err!=nil{problem(w,422,"invalid_node",err);return};writeJSON(w,201,output(node))}
func (s *Server) getNode(w http.ResponseWriter,r *http.Request){node,err:=s.nodes.Node(r.Context(),r.PathValue("id"));if errors.Is(err,storepkg.ErrNotFound){problem(w,404,"not_found",err);return};if err!=nil{problem(w,500,"read_failed",err);return};writeJSON(w,200,output(node))}
func (s *Server) updateNode(w http.ResponseWriter,r *http.Request){var in nodeInput;if err:=decode(r,&in);err!=nil{problem(w,400,"invalid_json",err);return};in.ID=r.PathValue("id");node:=in.domain();if err:=s.nodes.SaveNode(r.Context(),node);err!=nil{problem(w,422,"invalid_node",err);return};writeJSON(w,200,output(node))}
func (s *Server) deleteNode(w http.ResponseWriter,r *http.Request){err:=s.nodes.DeleteNode(r.Context(),r.PathValue("id"));if errors.Is(err,storepkg.ErrNotFound){problem(w,404,"not_found",err);return};if err!=nil{problem(w,500,"delete_failed",err);return};w.WriteHeader(204)}
func decode(r *http.Request,dst any) error{dec:=json.NewDecoder(io.LimitReader(r.Body,1<<20));dec.DisallowUnknownFields();if err:=dec.Decode(dst);err!=nil{return err};var extra any;if err:=dec.Decode(&extra);!errors.Is(err,io.EOF){return errors.New("multiple JSON values")};return nil}
func randomID() string{var body [16]byte;if _,err:=rand.Read(body[:]);err!=nil{panic("secure random unavailable")};return hex.EncodeToString(body[:])}
func writeJSON(w http.ResponseWriter,status int,value any){w.Header().Set("Content-Type","application/json; charset=utf-8");w.WriteHeader(status);_ = json.NewEncoder(w).Encode(value)}
func problem(w http.ResponseWriter,status int,code string,err error){writeJSON(w,status,map[string]any{"error":code,"message":err.Error()})}
