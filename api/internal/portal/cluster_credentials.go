package portal

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func legacyClusterCipher() (cipher.AEAD, error) {
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
	return newClusterCipher(key)
}

func newClusterCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("集群凭据存储密钥无效")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

const sqliteCredentialPrefix = "v2:"

// storedClusterCipher 读写与 SQLite 同目录的密钥，避免密钥和凭据密文存放在同一文件。
func (o *onboarding) storedClusterCipher(ctx context.Context, create bool) (cipher.AEAD, error) {
	path := o.store.Path() + ".cluster-credentials.key"
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && create {
		var occupied bool
		err = o.store.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM managed_clusters WHERE json_extract(document, '$.credentialCipher') LIKE 'v2:%'
		)`).Scan(&occupied)
		if err != nil {
			return nil, err
		}
		if occupied {
			// 先重读一次，处理另一个进程刚完成首次保存的情况。
			key, err = os.ReadFile(path)
		} else {
			key, err = createClusterKey(path)
		}
	}
	if err != nil {
		return nil, err
	}
	return newClusterCipher(key)
}

// createClusterKey 先写好并同步密钥，再原子发布文件；并发初始化沿用同一把密钥。
func createClusterKey(path string) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".cluster-credentials-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(key); err != nil {
		file.Close()
		return nil, err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return nil, err
	}
	if err := os.Link(file.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return os.ReadFile(path)
		}
		return nil, err
	}
	return key, nil
}

func clusterCredentialContext(spec managedCluster) []byte {
	raw, _ := json.Marshal([3]string{"kube-aiops/cluster-credentials/v1", spec.ID, spec.APIURL})
	return raw
}

// prepareCredentials 解析新凭据草稿，未提交草稿时沿用原有加密凭据。
func (spec *managedCluster) prepareCredentials(draft *credentialInput, previous managedCluster) (*clusterCredential, error) {
	spec.CredentialKind, spec.CredentialCipher = previous.CredentialKind, previous.CredentialCipher
	if draft == nil {
		if spec.CredentialCipher != "" && spec.APIURL != previous.APIURL {
			return nil, errors.New("修改直连地址时必须提供新的凭据")
		}
		return nil, nil
	}
	if spec.Mode != "pull" {
		return nil, errors.New("直连凭据仅适用于外部直连")
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
			return nil, err
		}
		spec.APIURL, entry = endpoint, credential
	default:
		return nil, errors.New("不支持该直连凭据方式")
	}
	if !remoteHTTPS(spec.APIURL) {
		return nil, errors.New("必须使用 HTTPS Kubernetes API 地址")
	}
	return &entry, nil
}

// encryptCredential 仅在保存或轮换时生成绑定到实例 ID 和 API 地址的密文。
func (o *onboarding) encryptCredential(ctx context.Context, spec *managedCluster, kind string, entry *clusterCredential) error {
	aead, err := o.storedClusterCipher(ctx, true)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(entry)
	spec.CredentialKind = kind
	spec.CredentialCipher = sqliteCredentialPrefix + base64.StdEncoding.EncodeToString(aead.Seal(nil, nil, raw, clusterCredentialContext(*spec)))
	return nil
}

// configureCredential 解密 SQLite 中的直连凭据，并兼容升级前的密文。
func (o *onboarding) configureCredential(spec managedCluster, cfg *kubernetes.Config) error {
	rawCipher := spec.CredentialCipher
	if rawCipher == "" {
		return errors.New("请填写外部直连凭据")
	}
	var aead cipher.AEAD
	var err error
	if cipherText, current := strings.CutPrefix(rawCipher, sqliteCredentialPrefix); current {
		rawCipher = cipherText
		aead, err = o.storedClusterCipher(context.Background(), false)
	} else {
		aead, err = legacyClusterCipher()
	}
	if err != nil {
		return errors.New("无法读取已保存的集群凭据，请检查持久化密钥或重新录入凭据")
	}
	encrypted, err := base64.StdEncoding.DecodeString(rawCipher)
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
