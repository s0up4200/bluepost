package model

import "time"

type Message struct {
	Kind            string    `json:"kind"`
	Handle          string    `json:"handle"`
	SenderAddress   string    `json:"sender_address"`
	SenderPhoneNorm string    `json:"sender_phone_norm"`
	ContactName     string    `json:"contact_name,omitempty"`
	Body            string    `json:"body"`
	Timestamp       time.Time `json:"timestamp"`
	Read            bool      `json:"read"`
}

type Contact struct {
	Name   string   `json:"name"`
	Phones []string `json:"phones"`
	Emails []string `json:"emails"`
}

type Status struct {
	State        string `json:"state"`
	Detail       string `json:"detail"`
	MAP          bool   `json:"map"`
	PBAP         bool   `json:"pbap"`
	Storage      string `json:"storage"`
	Phone        string `json:"phone"`
	HistoryCount int    `json:"history_count"`
	ContactCount int    `json:"contact_count"`
}
