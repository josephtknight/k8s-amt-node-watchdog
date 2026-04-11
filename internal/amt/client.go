package amt

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/icholy/digest"
)

// PowerCycler is the interface for power-cycling a node via AMT.
type PowerCycler interface {
	PowerCycle(ip string) error
}

// Client is a minimal AMT WSMAN client that sends a single power-cycle command.
type Client struct {
	username string
	password string
	port     int
	client   *http.Client
}

func NewClient(username, password string, port int) *Client {
	return &Client{
		username: username,
		password: password,
		port:     port,
		client: &http.Client{
			Transport: &digest.Transport{
				Username: username,
				Password: password,
			},
			Timeout: 30 * time.Second,
		},
	}
}

// PowerCycle sends a CIM_PowerManagementService.RequestPowerStateChange with PowerState=5
// (Power Cycle - Off Soft) to the AMT endpoint at the given IP.
func (c *Client) PowerCycle(ip string) error {
	url := fmt.Sprintf("http://%s:%d/wsman", ip, c.port)
	slog.Info("sending AMT power cycle command", "url", url)

	req, err := http.NewRequest("POST", url, strings.NewReader(powerCycleSOAP))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("AMT request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("AMT returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Check for ReturnValue in the response.
	// A ReturnValue of 0 means success.
	bodyStr := string(body)
	if strings.Contains(bodyStr, "<ReturnValue>0</ReturnValue>") {
		slog.Info("AMT power cycle command accepted", "ip", ip)
		return nil
	}

	return fmt.Errorf("AMT power cycle failed, response: %s", bodyStr)
}

const powerCycleSOAP = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing"
            xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd"
            xmlns:p="http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_PowerManagementService">
  <s:Header>
    <a:Action>http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_PowerManagementService/RequestPowerStateChange</a:Action>
    <a:To>/wsman</a:To>
    <w:ResourceURI>http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_PowerManagementService</w:ResourceURI>
    <a:MessageID>1</a:MessageID>
    <a:ReplyTo>
      <a:Address>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:Address>
    </a:ReplyTo>
    <w:SelectorSet>
      <w:Selector Name="Name">Intel(r) AMT Power Management Service</w:Selector>
      <w:Selector Name="SystemName">Intel(r) AMT</w:Selector>
      <w:Selector Name="CreationClassName">CIM_PowerManagementService</w:Selector>
      <w:Selector Name="SystemCreationClassName">CIM_ComputerSystem</w:Selector>
    </w:SelectorSet>
  </s:Header>
  <s:Body>
    <p:RequestPowerStateChange_INPUT>
      <p:PowerState>5</p:PowerState>
      <p:ManagedElement>
        <a:Address>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:Address>
        <a:ReferenceParameters>
          <w:ResourceURI>http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_ComputerSystem</w:ResourceURI>
          <w:SelectorSet>
            <w:Selector Name="Name">ManagedSystem</w:Selector>
            <w:Selector Name="CreationClassName">CIM_ComputerSystem</w:Selector>
          </w:SelectorSet>
        </a:ReferenceParameters>
      </p:ManagedElement>
    </p:RequestPowerStateChange_INPUT>
  </s:Body>
</s:Envelope>`
