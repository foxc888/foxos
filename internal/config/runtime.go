package config

import (
	"errors"
	"net/url"
	"os"
	"strings"
)

type Runtime struct {
	APIToken string
	RouterOS Endpoint
	Mihomo Mihomo
}

type Endpoint struct {
	URL string
	Username string
	Password string
}

type Mihomo struct {
	URL string
	Secret string
	LocalConfigPath string
	RuntimeConfigPath string
	BackupDir string
}

func Load() (Runtime,error) {
	cfg:=Runtime{
		APIToken:os.Getenv("FOXOS_API_TOKEN"),
		RouterOS:Endpoint{URL:os.Getenv("FOXOS_ROUTEROS_URL"),Username:os.Getenv("FOXOS_ROUTEROS_USERNAME"),Password:os.Getenv("FOXOS_ROUTEROS_PASSWORD")},
		Mihomo:Mihomo{URL:os.Getenv("FOXOS_MIHOMO_URL"),Secret:os.Getenv("FOXOS_MIHOMO_SECRET"),LocalConfigPath:os.Getenv("FOXOS_MIHOMO_LOCAL_CONFIG"),RuntimeConfigPath:os.Getenv("FOXOS_MIHOMO_RUNTIME_CONFIG"),BackupDir:os.Getenv("FOXOS_MIHOMO_BACKUP_DIR")},
	}
	if len(cfg.APIToken)<32{return Runtime{},errors.New("FOXOS_API_TOKEN must contain at least 32 characters")}
	if err:=validateOptionalEndpoint(cfg.RouterOS.URL);err!=nil{return Runtime{},errors.New("invalid FOXOS_ROUTEROS_URL")}
	if cfg.RouterOS.URL!=""&&(cfg.RouterOS.Username==""||cfg.RouterOS.Password==""){return Runtime{},errors.New("RouterOS credentials are required when RouterOS is configured")}
	if err:=validateOptionalEndpoint(cfg.Mihomo.URL);err!=nil{return Runtime{},errors.New("invalid FOXOS_MIHOMO_URL")}
	if cfg.Mihomo.URL!=""&&(cfg.Mihomo.LocalConfigPath==""||cfg.Mihomo.RuntimeConfigPath==""||cfg.Mihomo.BackupDir==""){return Runtime{},errors.New("Mihomo config and backup paths are required when Mihomo is configured")}
	return cfg,nil
}
func validateOptionalEndpoint(value string)error{
	if value==""{return nil}
	if strings.TrimSpace(value)!=value{return errors.New("endpoint contains surrounding whitespace")}
	parsed,err:=url.Parse(value);if err!=nil||parsed.Host==""||(parsed.Scheme!="http"&&parsed.Scheme!="https")||parsed.User!=nil{return errors.New("invalid endpoint")}
	return nil
}
