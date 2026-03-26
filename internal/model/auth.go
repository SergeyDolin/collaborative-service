package model

type AuthRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Message string `json:"message"`
	Login   string `json:"login"`
}
