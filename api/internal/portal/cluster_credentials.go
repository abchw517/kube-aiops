package portal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
	"gopkg.in/yaml.v3"
)

type credentialInput struct {
	Kind        string `json:"kind"`
	Token       string `json:"token"`
	CA          string `json:"ca"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"privateKey"`
	Kubeconfig  string `json:"kubeconfig"`
}

type clusterCredential struct {
	Token       string
	CA          []byte
	Certificate []byte
	Key         []byte
}

func clusterCipher() (cipher.AEAD, error) {
	path := os.Getenv("CLUSTER_CREDENTIAL_KEY_FILE")
	if path == "" {
		path = os.Getenv("AUTH_LDAP_ENCRYPTION_KEY_FILE")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("请配置集群凭据加密密钥")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("集群凭据密钥必须为 Base64 编码的 32 字节")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func clusterCredentialContext(spec managedCluster) []byte {
	raw, _ := json.Marshal([3]string{"kube-aiops/cluster-credentials/v1", spec.ID, spec.APIURL})
	return raw
}

func (spec *managedCluster) useCredentials(draft *credentialInput, previous managedCluster) error {
	spec.CredentialKind, spec.CredentialCipher = previous.CredentialKind, previous.CredentialCipher
	if draft == nil {
		if spec.CredentialCipher != "" && spec.APIURL != previous.APIURL {
			return errors.New("修改直连地址时必须提供新的凭据")
		}
		return nil
	}
	if spec.Mode != "pull" {
		return errors.New("直连凭据仅适用于外部直连")
	}
	entry := clusterCredential{Token: strings.TrimSpace(draft.Token), CA: []byte(draft.CA), Certificate: []byte(draft.Certificate), Key: []byte(draft.PrivateKey)}
	switch draft.Kind {
	case "token":
		entry.Certificate, entry.Key = nil, nil
	case "certificate":
		entry.Token = ""
	case "kubeconfig":
		endpoint, credential, err := decodeKubeconfig(draft.Kubeconfig)
		if err != nil {
			return err
		}
		spec.APIURL, entry = endpoint, credential
	default:
		return errors.New("不支持该直连凭据方式")
	}
	if !remoteHTTPS(spec.APIURL) {
		return errors.New("必须使用 HTTPS Kubernetes API 地址")
	}
	if err := entry.apply(new(kubernetes.Config)); err != nil {
		return err
	}
	aead, err := clusterCipher()
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(entry)
	spec.CredentialKind = draft.Kind
	spec.CredentialCipher = base64.StdEncoding.EncodeToString(aead.Seal(nil, nil, raw, clusterCredentialContext(*spec)))
	return nil
}

func (spec managedCluster) configureCredential(cfg *kubernetes.Config) error {
	if spec.CredentialCipher == "" {
		return nil
	}
	aead, err := clusterCipher()
	if err != nil {
		return err
	}
	encrypted, err := base64.StdEncoding.DecodeString(spec.CredentialCipher)
	if err != nil {
		return errors.New("直连凭据无法读取")
	}
	raw, err := aead.Open(nil, nil, encrypted, clusterCredentialContext(spec))
	var entry clusterCredential
	if err != nil || json.Unmarshal(raw, &entry) != nil {
		return errors.New("直连凭据无法解密")
	}
	return entry.apply(cfg)
}

func (c clusterCredential) apply(cfg *kubernetes.Config) error {
	if len(c.CA) == 0 || !x509.NewCertPool().AppendCertsFromPEM(c.CA) {
		return errors.New("请输入有效的 Kubernetes CA PEM")
	}
	cfg.CAData = c.CA
	if len(c.Certificate) > 0 || len(c.Key) > 0 {
		pair, err := tls.X509KeyPair(c.Certificate, c.Key)
		if err != nil {
			return errors.New("客户端证书或私钥无效")
		}
		cfg.ClientCertificate = &pair
	} else if c.Token == "" || strings.ContainsAny(c.Token, "\r\n") {
		return errors.New("必须提供有效的只读 Token 或客户端证书")
	}
	cfg.Token = c.Token
	return nil
}

func decodeKubeconfig(raw string) (string, clusterCredential, error) {
	var data struct {
		Version  string `yaml:"apiVersion"`
		Kind     string
		Current  string `yaml:"current-context"`
		Clusters []struct {
			Name    string
			Cluster struct {
				Server   string
				CA       string `yaml:"certificate-authority-data"`
				Insecure bool   `yaml:"insecure-skip-tls-verify"`
				Proxy    string `yaml:"proxy-url"`
				TLSName  string `yaml:"tls-server-name"`
			}
		}
		Contexts []struct {
			Name    string
			Context struct{ Cluster, User string }
		}
		Users []struct {
			Name string
			User struct {
				Token    string
				Cert     string `yaml:"client-certificate-data"`
				Key      string `yaml:"client-key-data"`
				Exec     any
				Provider any `yaml:"auth-provider"`
			}
		}
	}
	invalid := errors.New("kubeconfig 缺少有效当前上下文、内嵌 CA 或静态凭据")
	var entry clusterCredential
	if len(raw) > 384*1024 || yaml.Unmarshal([]byte(raw), &data) != nil || data.Version != "v1" || data.Kind != "Config" || data.Current == "" {
		return "", entry, invalid
	}
	cluster, user := "", ""
	for _, c := range data.Contexts {
		if c.Name == data.Current {
			if cluster != "" || user != "" {
				return "", entry, invalid
			}
			cluster, user = c.Context.Cluster, c.Context.User
		}
	}
	if cluster == "" || user == "" {
		return "", entry, invalid
	}
	server := ""
	for _, c := range data.Clusters {
		if c.Name == cluster {
			if server != "" || c.Cluster.Insecure || c.Cluster.Proxy != "" || c.Cluster.TLSName != "" || !remoteHTTPS(c.Cluster.Server) {
				return "", entry, invalid
			}
			var err error
			server = c.Cluster.Server
			entry.CA, err = base64.StdEncoding.DecodeString(c.Cluster.CA)
			if err != nil {
				return "", entry, invalid
			}
		}
	}
	if server == "" {
		return "", entry, invalid
	}
	found := false
	for _, u := range data.Users {
		if u.Name == user {
			if found || u.User.Exec != nil || u.User.Provider != nil {
				return "", entry, invalid
			}
			found = true
			entry.Token = strings.TrimSpace(u.User.Token)
			var err error
			entry.Certificate, err = base64.StdEncoding.DecodeString(u.User.Cert)
			if err != nil {
				return "", entry, invalid
			}
			entry.Key, err = base64.StdEncoding.DecodeString(u.User.Key)
			if err != nil {
				return "", entry, invalid
			}
		}
	}
	if !found {
		return "", entry, invalid
	}
	return server, entry, nil
}
