package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ServiceClient — HTTP-клиент для сервиса коллаборативного позиционирования
type ServiceClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewServiceClient(baseURL, token string) *ServiceClient {
	return &ServiceClient{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// ----------------------------------------------------------------
// Auth
// ----------------------------------------------------------------

type loginReq struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type loginResp struct {
	Token string `json:"token"`
}

func (c *ServiceClient) Login(baseURL, username, password string) (string, error) {
	c.baseURL = baseURL
	var resp loginResp
	if err := c.post("/api/login", loginReq{Login: username, Password: password}, &resp, false); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("сервер не вернул токен")
	}
	c.token = resp.Token
	return resp.Token, nil
}

// ----------------------------------------------------------------
// Online session
// ----------------------------------------------------------------

type registerOnlineReq struct {
	PublicIP        string  `json:"publicIP"`
	OutputPort      int     `json:"outputPort"`
	SolutionPort    int     `json:"solutionPort"`
	RoughenedLat    float64 `json:"roughenedLat"`
	RoughenedLon    float64 `json:"roughenedLon"`
	RoughenedHeight float64 `json:"roughenedHeight"`
}

type RegisterOnlineResp struct {
	SessionID int    `json:"sessionId"`
	EPHHost   string `json:"ephHost"`
	EPHPort   int    `json:"ephPort"`
	SSRHost   string `json:"ssrHost"`
	SSRPort   int    `json:"ssrPort"`
}

func (c *ServiceClient) RegisterSession(
	publicIP string, outputPort, solutionPort int,
	lat, lon, height float64,
) (*RegisterOnlineResp, error) {
	var resp RegisterOnlineResp
	err := c.post("/api/online/register", registerOnlineReq{
		PublicIP:        publicIP,
		OutputPort:      outputPort,
		SolutionPort:    solutionPort,
		RoughenedLat:    lat,
		RoughenedLon:    lon,
		RoughenedHeight: height,
	}, &resp, true)
	return &resp, err
}

type updateStatusReq struct {
	ConvergenceStatus int     `json:"convergenceStatus"`
	IsMoving          bool    `json:"isMoving"`
	RoughenedLat      float64 `json:"roughenedLat"`
	RoughenedLon      float64 `json:"roughenedLon"`
}

type candidateJSON struct {
	IP           string `json:"ip"`
	Port         int    `json:"port"`
	SolutionPort int    `json:"solutionPort"`
	IsMoving     bool   `json:"isMoving"`
}

type updateStatusResp struct {
	Candidate *candidateJSON `json:"candidate,omitempty"`
}

type CandidateInfo struct {
	IP           string
	Port         int
	SolutionPort int
	IsMoving     bool
}

func (c *ServiceClient) UpdateStatus(convergence int, isMoving bool, lat, lon float64) (*CandidateInfo, error) {
	var resp updateStatusResp
	err := c.post("/api/online/status", updateStatusReq{
		ConvergenceStatus: convergence,
		IsMoving:          isMoving,
		RoughenedLat:      lat,
		RoughenedLon:      lon,
	}, &resp, true)
	if err != nil {
		return nil, err
	}
	if resp.Candidate != nil {
		return &CandidateInfo{
			IP:           resp.Candidate.IP,
			Port:         resp.Candidate.Port,
			SolutionPort: resp.Candidate.SolutionPort,
			IsMoving:     resp.Candidate.IsMoving,
		}, nil
	}
	return nil, nil
}

func (c *ServiceClient) DeleteSession() error {
	req, _ := http.NewRequest(http.MethodDelete, c.baseURL+"/api/online/session", nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ----------------------------------------------------------------
// Devices
// ----------------------------------------------------------------

// DeviceInfo — информация об устройстве с сервиса
type DeviceInfo struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	DeviceType   string  `json:"deviceType"`
	AntennaName  string  `json:"antennaName"`
	AntennaE     float64 `json:"antennaE"`
	AntennaN     float64 `json:"antennaN"`
	AntennaU     float64 `json:"antennaU"`
	ReceiverType string  `json:"receiverType"` // tcp / ntrip / serial
	ReceiverHost string  `json:"receiverHost"`
	ReceiverPort int     `json:"receiverPort"`
	ReceiverMount string `json:"receiverMount"`
	ReceiverUser string  `json:"receiverUser"`
	ReceiverPass string  `json:"receiverPass"`
}

func (d DeviceInfo) ReceiverPath() string {
	switch d.ReceiverType {
	case "ntrip":
		if d.ReceiverUser != "" {
			return fmt.Sprintf("%s:%s@%s:%d/%s", d.ReceiverUser, d.ReceiverPass, d.ReceiverHost, d.ReceiverPort, d.ReceiverMount)
		}
		return fmt.Sprintf("%s:%d/%s", d.ReceiverHost, d.ReceiverPort, d.ReceiverMount)
	case "serial":
		baud := d.ReceiverPort
		if baud == 0 {
			baud = 115200
		}
		return fmt.Sprintf("%s:%d:8:n:1:off", d.ReceiverHost, baud)
	default: // tcp
		return fmt.Sprintf("%s:%d", d.ReceiverHost, d.ReceiverPort)
	}
}

func (c *ServiceClient) GetDevices() ([]DeviceInfo, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/devices", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var devices []DeviceInfo
	if err := json.Unmarshal(raw, &devices); err != nil {
		return nil, err
	}
	return devices, nil
}

// ----------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------

func (c *ServiceClient) post(path string, body, dest any, auth bool) error {
	payload, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}
	if dest != nil {
		return json.Unmarshal(raw, dest)
	}
	return nil
}

// GetPublicIP определяет публичный IP через ipify.org
func GetPublicIP() string {
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("https://api.ipify.org")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
