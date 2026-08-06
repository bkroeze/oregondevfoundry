package notifications

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mailgun/mailgun-go/v4"
)

const (
	OrderStatusTemplate = "order-status"
	InvitationTemplate  = "purchase-account-invitation"
)

type ReceiptLine struct {
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	Quantity  int64  `json:"quantity"`
	UnitCents int64  `json:"unit_cents"`
	LineCents int64  `json:"line_cents"`
}

type OrderStatus struct {
	To                 string        `json:"to"`
	PurchaserName      string        `json:"purchaser_name"`
	ConfirmationNumber string        `json:"confirmation_number"`
	Status             string        `json:"status"`
	PurchaseType       string        `json:"purchase_type"`
	Currency           string        `json:"currency"`
	SubtotalCents      int64         `json:"subtotal_cents"`
	ShippingCents      int64         `json:"shipping_cents"`
	TotalCents         int64         `json:"total_cents"`
	Lines              []ReceiptLine `json:"lines"`
}

type Invitation struct {
	To            string `json:"to"`
	PurchaserName string `json:"purchaser_name"`
	ActivationURL string `json:"activation_url"`
}

type Mailer interface {
	SendOrderStatus(context.Context, OrderStatus) error
	SendInvitation(context.Context, Invitation) error
}

type MailgunMailer struct {
	client *mailgun.MailgunImpl
	from   string
}

func NewMailgunMailer(domain, apiKey, region, from string) *MailgunMailer {
	client := mailgun.NewMailgun(strings.TrimSpace(domain), strings.TrimSpace(apiKey))
	if strings.EqualFold(strings.TrimSpace(region), "eu") {
		client.SetAPIBase(mailgun.APIBaseEU)
	}
	return &MailgunMailer{client: client, from: strings.TrimSpace(from)}
}

func (m *MailgunMailer) SendOrderStatus(ctx context.Context, receipt OrderStatus) error {
	if strings.TrimSpace(receipt.To) == "" || strings.TrimSpace(receipt.ConfirmationNumber) == "" {
		return errors.New("purchaser email and confirmation number are required")
	}
	message := m.client.NewMessage(m.from, fmt.Sprintf("Order %s: %s", receipt.ConfirmationNumber, receipt.Status), "", receipt.To)
	message.SetTemplate(OrderStatusTemplate)
	for name, value := range map[string]any{
		"purchaser_name":      receipt.PurchaserName,
		"confirmation_number": receipt.ConfirmationNumber,
		"status":              receipt.Status,
		"purchase_type":       receipt.PurchaseType,
		"currency":            receipt.Currency,
		"subtotal_cents":      receipt.SubtotalCents,
		"shipping_cents":      receipt.ShippingCents,
		"total_cents":         receipt.TotalCents,
		"lines":               receipt.Lines,
	} {
		if err := message.AddTemplateVariable(name, value); err != nil {
			return fmt.Errorf("set Mailgun order template variable: %w", err)
		}
	}
	if _, _, err := m.client.Send(ctx, message); err != nil {
		return fmt.Errorf("send Mailgun order status: %w", err)
	}
	return nil
}

func (m *MailgunMailer) SendInvitation(ctx context.Context, invitation Invitation) error {
	if strings.TrimSpace(invitation.To) == "" || strings.TrimSpace(invitation.ActivationURL) == "" {
		return errors.New("invitation email and activation URL are required")
	}
	message := m.client.NewMessage(m.from, "Finish creating your Oregon Dev Foundry account", "", invitation.To)
	message.SetTemplate(InvitationTemplate)
	if err := message.AddTemplateVariable("purchaser_name", invitation.PurchaserName); err != nil {
		return fmt.Errorf("set Mailgun invitation name: %w", err)
	}
	if err := message.AddTemplateVariable("activation_url", invitation.ActivationURL); err != nil {
		return fmt.Errorf("set Mailgun invitation URL: %w", err)
	}
	if _, _, err := m.client.Send(ctx, message); err != nil {
		return fmt.Errorf("send Mailgun invitation: %w", err)
	}
	return nil
}
