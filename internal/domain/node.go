package domain

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

var ErrInvalidNode = errors.New("invalid proxy node")

type Node struct {
	ID             string
	Name           string
	Type           string
	Server         string
	Port           int
	Username       string
	Password       string // #nosec G117 -- persisted credential material is exposed only through redacted API output.
	UUID           string
	Cipher         string
	Network        string
	SNI            string
	Path           string
	Host           string
	UDP            bool
	TLS            bool
	SkipCertVerify bool
	SubscriptionID string
	Extra          map[string]any
}

func (n Node) Validate() error {
	n.Type = strings.ToLower(strings.TrimSpace(n.Type))
	if strings.TrimSpace(n.ID) == "" || strings.TrimSpace(n.Name) == "" {
		return fmt.Errorf("%w: id and name are required", ErrInvalidNode)
	}
	if net.ParseIP(n.Server) == nil && strings.TrimSpace(n.Server) == "" {
		return fmt.Errorf("%w: server is required", ErrInvalidNode)
	}
	if n.Port < 1 || n.Port > 65535 {
		return fmt.Errorf("%w: port out of range", ErrInvalidNode)
	}
	switch n.Type {
	case "ss":
		if n.Cipher == "" || n.Password == "" {
			return fmt.Errorf("%w: ss cipher and password are required", ErrInvalidNode)
		}
	case "vmess", "vless":
		if n.UUID == "" {
			return fmt.Errorf("%w: %s uuid is required", ErrInvalidNode, n.Type)
		}
	case "trojan", "hysteria2", "socks5", "http":
		if (n.Type == "trojan" || n.Type == "hysteria2") && n.Password == "" {
			return fmt.Errorf("%w: %s password is required", ErrInvalidNode, n.Type)
		}
	case "tuic":
		if n.UUID == "" || n.Password == "" {
			return fmt.Errorf("%w: tuic uuid and password are required", ErrInvalidNode)
		}
	case "wireguard":
		privateKey, privateOK := n.Extra["private-key"].(string)
		publicKey, publicOK := n.Extra["public-key"].(string)
		if !privateOK || !publicOK || strings.TrimSpace(privateKey) == "" || strings.TrimSpace(publicKey) == "" {
			return fmt.Errorf("%w: wireguard private-key and public-key are required", ErrInvalidNode)
		}
	default:
		return fmt.Errorf("%w: unsupported type %q", ErrInvalidNode, n.Type)
	}
	return nil
}
