package assetmeta

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func rpcEscape(v string) string { return strings.ReplaceAll(url.QueryEscape(v), "+", "%20") }

func (c *Client) aliyun(ctx context.Context, cfg Config, keys Credentials) ([]Asset, error) {
	ids, _ := json.Marshal(cfg.AssetIDs)
	query := url.Values{
		"Action": {"DescribeInstances"}, "Version": {"2014-05-26"}, "Format": {"JSON"}, "RegionId": {cfg.Region},
		"InstanceIds": {string(ids)}, "PageSize": {"50"}, "SignatureMethod": {"HMAC-SHA1"}, "SignatureVersion": {"1.0"},
		"SignatureNonce": {rand.Text()}, "AccessKeyId": {keys.AccessKeyID}, "Timestamp": {time.Now().UTC().Format(time.RFC3339)},
	}
	if keys.SessionToken != "" {
		query.Set("SecurityToken", keys.SessionToken)
	}
	canonical := strings.ReplaceAll(query.Encode(), "+", "%20")
	sign := hmac.New(sha1.New, []byte(keys.AccessKeySecret+"&"))
	sign.Write([]byte("GET&%2F&" + rpcEscape(canonical)))
	query.Set("Signature", base64.StdEncoding.EncodeToString(sign.Sum(nil)))
	server := "https://ecs." + cfg.Region + ".aliyuncs.com/?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server, nil)
	if err != nil {
		return nil, ErrConfiguration
	}
	var result struct {
		Code      string
		Instances struct {
			Instance []struct{ InstanceId, InstanceName, Status string }
		}
	}
	if err = c.request(req, &result); err != nil {
		return nil, err
	}
	if result.Code != "" {
		return nil, ErrAuthorization
	}
	if result.Instances.Instance == nil {
		return nil, ErrUnavailable
	}
	out := []Asset{}
	for _, h := range result.Instances.Instance {
		out = append(out, Asset{ExternalID: h.InstanceId, Name: h.InstanceName, Kind: "ECS", State: h.Status})
	}
	return out, nil
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func mac256(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(value))
	return h.Sum(nil)
}

func tencentAuthorization(host string, body []byte, keys Credentials, now time.Time) string {
	const algorithm = "TC3-HMAC-SHA256"
	const headers = "content-type;host;x-tc-action"
	date := now.UTC().Format(time.DateOnly)
	scope := date + "/cvm/tc3_request"
	can := "POST\n/\n\ncontent-type:application/json; charset=utf-8\nhost:" + host + "\nx-tc-action:describeinstances\n\n" + headers + "\n" + digest(body)
	toSign := algorithm + "\n" + strconv.FormatInt(now.Unix(), 10) + "\n" + scope + "\n" + digest([]byte(can))
	signature := mac256(mac256(mac256(mac256([]byte("TC3"+keys.AccessKeySecret), date), "cvm"), "tc3_request"), toSign)
	return fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%x", algorithm, keys.AccessKeyID, scope, headers, signature)
}

func (c *Client) tencent(ctx context.Context, cfg Config, keys Credentials) ([]Asset, error) {
	const host = "cvm.tencentcloudapi.com"
	now := time.Now().UTC()
	body, _ := json.Marshal(map[string]any{"InstanceIds": cfg.AssetIDs, "Limit": 50})
	req, err := jsonRequest(ctx, "https://"+host+"/", body)
	if err != nil {
		return nil, ErrConfiguration
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-TC-Action", "DescribeInstances")
	req.Header.Set("X-TC-Version", "2017-03-12")
	req.Header.Set("X-TC-Region", cfg.Region)
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(now.Unix(), 10))
	req.Header.Set("Authorization", tencentAuthorization(host, body, keys, now))
	if keys.SessionToken != "" {
		req.Header.Set("X-TC-Token", keys.SessionToken)
	}
	var result struct {
		Response struct {
			Error       *struct{ Code string }
			InstanceSet []struct{ InstanceId, InstanceName, InstanceState string }
		}
	}
	if err = c.request(req, &result); err != nil {
		return nil, err
	}
	if result.Response.Error != nil {
		return nil, ErrAuthorization
	}
	if result.Response.InstanceSet == nil {
		return nil, ErrUnavailable
	}
	out := []Asset{}
	for _, h := range result.Response.InstanceSet {
		out = append(out, Asset{ExternalID: h.InstanceId, Name: h.InstanceName, Kind: "CVM", State: h.InstanceState})
	}
	return out, nil
}
