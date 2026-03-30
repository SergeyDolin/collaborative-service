package model

type AuthRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Message string `json:"message,omitempty"`
	Login   string `json:"login,omitempty"`
	Token   string `json:"token,omitempty"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
