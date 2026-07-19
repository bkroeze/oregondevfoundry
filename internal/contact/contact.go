package contact

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/mailgun/mailgun-go/v4"
)

type Message struct {
	Name, Email, Company, Body string
}

type Sender interface {
	Send(context.Context, Message) error
}

type MailgunSender struct {
	client *mailgun.MailgunImpl
	from   string
	to     string
}

func NewMailgunSender(domain, apiKey, region, from, to string) *MailgunSender {
	client := mailgun.NewMailgun(domain, apiKey)
	if region == "eu" {
		client.SetAPIBase(mailgun.APIBaseEU)
	}
	return &MailgunSender{client: client, from: from, to: to}
}

func (s *MailgunSender) Send(ctx context.Context, message Message) error {
	subject := fmt.Sprintf("Website inquiry from %s", message.Name)
	body := fmt.Sprintf("Name: %s\nEmail: %s\nCompany: %s\n\n%s", message.Name, message.Email, message.Company, message.Body)
	mailMessage := s.client.NewMessage(s.from, subject, body, s.to)
	mailMessage.SetReplyTo(message.Email)
	_, _, err := s.client.Send(ctx, mailMessage)
	return err
}

func Validate(message Message) map[string]string {
	errors := make(map[string]string)
	if message.Name == "" {
		errors["name"] = "Please enter your name."
	} else if utf8.RuneCountInString(message.Name) > 100 {
		errors["name"] = "Name is too long."
	}
	if address, err := mail.ParseAddress(message.Email); err != nil || address.Address != message.Email || utf8.RuneCountInString(message.Email) > 254 {
		errors["email"] = "Please enter a valid email address."
	}
	if utf8.RuneCountInString(message.Company) > 120 {
		errors["company"] = "Company is too long."
	}
	length := utf8.RuneCountInString(message.Body)
	if length < 20 {
		errors["message"] = "Please provide at least 20 characters."
	} else if length > 5000 {
		errors["message"] = "Message is too long."
	}
	return errors
}

func Clean(value string) string { return strings.TrimSpace(value) }
