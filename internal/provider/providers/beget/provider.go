package beget

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"

	"github.com/qdm12/ddns-updater/internal/models"
	"github.com/qdm12/ddns-updater/internal/provider/constants"
	"github.com/qdm12/ddns-updater/internal/provider/errors"
	"github.com/qdm12/ddns-updater/internal/provider/headers"
	"github.com/qdm12/ddns-updater/internal/provider/utils"
	"github.com/qdm12/ddns-updater/pkg/publicip/ipversion"
)

type Provider struct {
	domain string
	// Note: For some reason ddns-updater strips subdomains from "domain"
	// We introduce "target", which contains unstripped domain name
	target   string
	owner    string
	login    string
	password string
	priority int
}

type getDataResponse struct {
	Status string
	Answer struct {
		Status string
		Result struct {
			FQDN    string `json:"fqdn"`
			Records map[string]json.RawMessage
		}
	}
}

func New(data json.RawMessage, domain, owner string) (
	p *Provider, err error) {
	// TODO: is "owner" argument really needed?

	extraSettings := struct {
		Login    string `json:"login"`
		Password string `json:"password"`
		// "domain" arg has subdomains stripped, so parse it again
		Domain   string `json:"domain"`
		Priority int    `json:"priority"`
	}{}

	err = json.Unmarshal(data, &extraSettings)
	if err != nil {
		return nil, err
	}

	// validating settings
	err = utils.CheckDomain(extraSettings.Domain)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errors.ErrDomainNotValid, err)
	}

	return &Provider{
		domain:   domain,
		target:   extraSettings.Domain,
		priority: extraSettings.Priority,
		owner:    owner,
		login:    extraSettings.Login,
		password: extraSettings.Password,
	}, nil

}

// Next few functions were blindly adapted from other provider.go files.

func (p *Provider) String() string {
	return utils.ToString(p.domain, p.owner, constants.Beget, ipversion.IP4)
}

func (p *Provider) Domain() string {
	return p.domain
}

func (p *Provider) Owner() string {
	return p.owner
}

func (p *Provider) IPVersion() ipversion.IPVersion {
	return ipversion.IP4
}

func (p *Provider) IPv6Suffix() netip.Prefix {
	return netip.Prefix{}
}

func (p *Provider) Proxied() bool {
	return false
}

func (p *Provider) BuildDomainName() string {
	return utils.BuildDomainName(p.owner, p.domain)
}

func (p *Provider) HTML() models.HTMLRow {
	return models.HTMLRow{
		Domain:    fmt.Sprintf("<a href=\"http://%s\">%s</a>", p.BuildDomainName(), p.BuildDomainName()),
		Owner:     p.Owner(),
		Provider:  "<a href=\"https://beget.com\">Beget</a>",
		IPVersion: ipversion.IP4.String(),
	}
}

// apiCall performs authenticated GET request to the given URLEndpoint of api.beget.com
// with given inputJSON as input_data and returns resulting json as []byte.
func (p *Provider) apiCall(ctx context.Context, client *http.Client, URLEndpoint string, inputJSON []byte) ([]byte, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "api.beget.com",
		Path:   URLEndpoint,
	}

	v := url.Values{}
	v.Set("login", p.login)
	v.Set("passwd", p.password)
	v.Set("input_format", "json")
	v.Set("output_format", "json")
	v.Set("input_data", string(inputJSON))
	u.RawQuery = v.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return []byte{}, fmt.Errorf("%s: Failed creating HTTP request: %w", u.Path, err)
	}
	headers.SetUserAgent(request)
	headers.SetAccept(request, "application/json")

	response, err := client.Do(request)
	if err != nil {
		return []byte{}, fmt.Errorf("%s: Failed performing HTTP request: %w", u.Path, err)
	}
	defer response.Body.Close()

	b, err := io.ReadAll(response.Body)
	if err != nil {
		return []byte{}, fmt.Errorf("%s: Failed reading response body: %w", u.Path, err)
	}

	if response.StatusCode != http.StatusOK {
		return b, fmt.Errorf("%s: HTTP status is %d", u.Path, response.StatusCode)
	}

	return b, nil
}

func (p *Provider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (newIP netip.Addr, err error) {
	// Beget API DNS administration docs: https://beget.com/en/kb/api/dns-administration-functions

	// Before we call Beget API's /api/dns/changeRecords method, we need to fetch current DNS
	// configuration as setting A record alone will clear all other records for this domain.
	// This behavior is undocumented.

	// Part 1: getData
	getDataRequest, err := json.Marshal(map[string]string{"fqdn": p.target})
	if err != nil {
		return netip.Addr{}, fmt.Errorf("Couldn't marshal getData request: %w", err)
	}

	currentDataRaw, err := p.apiCall(ctx, client, "api/dns/getData", getDataRequest)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("Calling getData failed: %w", err)
	}

	currentDataStruct := getDataResponse{}
	err = json.Unmarshal(currentDataRaw, &currentDataStruct)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("Failed unmarshalling getData response: %w", err)
	}

	if currentDataStruct.Status != "success" || currentDataStruct.Answer.Status != "success" || currentDataStruct.Answer.Result.FQDN != p.target {
		return netip.Addr{}, fmt.Errorf("getData response doesn't indicate success")
	}

	// Part 2: changeRecords
	// Preparing request
	inputData := struct {
		FQDN    string                     `json:"fqdn"`
		Records map[string]json.RawMessage `json:"records"`
	}{
		FQDN:    p.target,
		Records: currentDataStruct.Answer.Result.Records,
	}
	newAEntryJSON, err := json.Marshal(
		[]struct {
			Priority int    `json:"priority"`
			Value    string `json:"value"`
		}{{Priority: p.priority, Value: ip.String()}})
	if err != nil {
		return netip.Addr{}, fmt.Errorf("Couldn't marshal new A entry JSON: %w", err)
	}
	inputData.Records["A"] = json.RawMessage(newAEntryJSON)

	inputDataRaw, err := json.Marshal(inputData)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("Couldn't marshal json: %w", err)
	}

	// Calling API & parsing response
	changeRecordsResponseRaw, err := p.apiCall(ctx, client, "/api/dns/changeRecords", inputDataRaw)

	result := struct {
		Status string
		Answer struct {
			Status string
			Result bool
		}
	}{}
	err = json.Unmarshal(changeRecordsResponseRaw, &result)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("Failed unmarshalling changeRecords response: %w", err)
	}

	if result.Status != "success" || result.Answer.Status != "success" {
		return netip.Addr{}, fmt.Errorf("changeRequest response doesn't indicate success: %s", utils.ToSingleLine(string(changeRecordsResponseRaw)))
	}
	return ip, nil
}
