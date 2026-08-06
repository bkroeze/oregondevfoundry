package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/client"
)

const (
	ModePayment      = "payment"
	ModeSubscription = "subscription"
)

type Line struct {
	Name        string
	Description string
	UnitCents   int64
	Quantity    int64
	Recurring   bool
}

type CreateSessionParams struct {
	Mode            string
	Currency        string
	CustomerEmail   string
	ClientReference string
	ReturnURL       string
	Metadata        map[string]string
	Lines           []Line
}

type Session struct {
	ID              string
	ClientSecret    string
	Mode            string
	PaymentStatus   string
	Status          string
	CustomerID      string
	CustomerEmail   string
	PaymentIntentID string
	SubscriptionID  string
	ClientReference string
	Metadata        map[string]string
}

type Event struct {
	ID      string
	Type    string
	Session Session
}

type Gateway interface {
	CreateSession(context.Context, CreateSessionParams) (Session, error)
	GetSession(context.Context, string) (Session, error)
	ExpireSession(context.Context, string) error
	CancelSubscription(context.Context, string) error
	ParseWebhook([]byte, string) (Event, error)
}

type StripeGateway struct {
	client        *client.API
	webhookSecret string
}

func NewStripeGateway(secretKey, webhookSecret string) *StripeGateway {
	return &StripeGateway{client: client.New(strings.TrimSpace(secretKey), nil), webhookSecret: strings.TrimSpace(webhookSecret)}
}

func (g *StripeGateway) CreateSession(ctx context.Context, in CreateSessionParams) (Session, error) {
	if in.Mode != ModePayment && in.Mode != ModeSubscription {
		return Session{}, errors.New("unsupported Stripe Checkout mode")
	}
	if strings.TrimSpace(in.Currency) == "" || strings.TrimSpace(in.ReturnURL) == "" || len(in.Lines) == 0 {
		return Session{}, errors.New("currency, return URL, and line items are required")
	}
	params := &stripe.CheckoutSessionParams{
		Mode:                 stripe.String(in.Mode),
		UIMode:               stripe.String("embedded"),
		ReturnURL:            stripe.String(in.ReturnURL),
		RedirectOnCompletion: stripe.String("always"),
		ClientReferenceID:    stripe.String(in.ClientReference),
		Metadata:             in.Metadata,
	}
	params.Context = ctx
	if email := strings.TrimSpace(in.CustomerEmail); email != "" {
		params.CustomerEmail = stripe.String(email)
	}
	if in.Mode == ModePayment {
		params.CustomerCreation = stripe.String("always")
	}
	for _, line := range in.Lines {
		if strings.TrimSpace(line.Name) == "" || line.UnitCents < 0 || line.Quantity < 1 {
			return Session{}, errors.New("invalid Stripe Checkout line item")
		}
		priceData := &stripe.CheckoutSessionLineItemPriceDataParams{
			Currency: stripe.String(strings.ToLower(in.Currency)),
			ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
				Name: stripe.String(line.Name),
			},
			UnitAmount: stripe.Int64(line.UnitCents),
		}
		if line.Description != "" {
			priceData.ProductData.Description = stripe.String(line.Description)
		}
		if line.Recurring {
			if in.Mode != ModeSubscription {
				return Session{}, errors.New("recurring line item requires subscription mode")
			}
			priceData.Recurring = &stripe.CheckoutSessionLineItemPriceDataRecurringParams{Interval: stripe.String("month")}
		} else if in.Mode == ModeSubscription {
			return Session{}, errors.New("subscription mode requires monthly recurring line items")
		}
		params.LineItems = append(params.LineItems, &stripe.CheckoutSessionLineItemParams{PriceData: priceData, Quantity: stripe.Int64(line.Quantity)})
	}
	created, err := g.client.CheckoutSessions.New(params)
	if err != nil {
		return Session{}, fmt.Errorf("create Stripe Checkout session: %w", err)
	}
	return sessionFromStripe(created), nil
}

func (g *StripeGateway) GetSession(ctx context.Context, id string) (Session, error) {
	if strings.TrimSpace(id) == "" {
		return Session{}, errors.New("Stripe session ID is required")
	}
	params := &stripe.CheckoutSessionParams{}
	params.Context = ctx
	found, err := g.client.CheckoutSessions.Get(id, params)
	if err != nil {
		return Session{}, fmt.Errorf("retrieve Stripe Checkout session: %w", err)
	}
	return sessionFromStripe(found), nil
}

func (g *StripeGateway) ExpireSession(ctx context.Context, id string) error {
	session, err := g.GetSession(ctx, id)
	if err != nil {
		return err
	}
	switch session.Status {
	case "expired":
		return nil
	case "open":
	case "complete":
		return errors.New("completed Stripe Checkout session cannot be canceled")
	default:
		return fmt.Errorf("cannot expire Stripe Checkout session in %q status", session.Status)
	}
	params := &stripe.CheckoutSessionExpireParams{}
	params.Context = ctx
	if _, err := g.client.CheckoutSessions.Expire(id, params); err != nil {
		return fmt.Errorf("expire Stripe Checkout session: %w", err)
	}
	return nil
}

func (g *StripeGateway) CancelSubscription(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("Stripe subscription ID is required")
	}
	params := &stripe.SubscriptionParams{}
	params.Context = ctx
	subscription, err := g.client.Subscriptions.Get(id, params)
	if err != nil {
		return fmt.Errorf("retrieve Stripe subscription: %w", err)
	}
	if string(subscription.Status) == "canceled" {
		return nil
	}
	cancelParams := &stripe.SubscriptionCancelParams{}
	cancelParams.Context = ctx
	if _, err := g.client.Subscriptions.Cancel(id, cancelParams); err != nil {
		return fmt.Errorf("cancel Stripe subscription: %w", err)
	}
	return nil
}

func (g *StripeGateway) ParseWebhook(payload []byte, signature string) (Event, error) {
	if g.webhookSecret == "" {
		return Event{}, errors.New("Stripe webhook secret is not configured")
	}
	event, err := stripe.ConstructEvent(payload, signature, g.webhookSecret)
	if err != nil {
		return Event{}, fmt.Errorf("verify Stripe webhook: %w", err)
	}
	result := Event{ID: event.ID, Type: string(event.Type)}
	if result.Type != "checkout.session.completed" && result.Type != "checkout.session.async_payment_succeeded" && result.Type != "checkout.session.async_payment_failed" && result.Type != "checkout.session.expired" {
		return result, nil
	}
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		return Event{}, fmt.Errorf("decode Stripe Checkout session: %w", err)
	}
	result.Session = sessionFromStripe(&session)
	return result, nil
}

func sessionFromStripe(session *stripe.CheckoutSession) Session {
	if session == nil {
		return Session{}
	}
	result := Session{
		ID:              session.ID,
		ClientSecret:    session.ClientSecret,
		Mode:            string(session.Mode),
		PaymentStatus:   string(session.PaymentStatus),
		Status:          string(session.Status),
		CustomerEmail:   session.CustomerEmail,
		ClientReference: session.ClientReferenceID,
		Metadata:        session.Metadata,
	}
	if session.Customer != nil {
		result.CustomerID = session.Customer.ID
	}
	if session.CustomerDetails != nil && session.CustomerDetails.Email != "" {
		result.CustomerEmail = session.CustomerDetails.Email
	}
	if session.PaymentIntent != nil {
		result.PaymentIntentID = session.PaymentIntent.ID
	}
	if session.Subscription != nil {
		result.SubscriptionID = session.Subscription.ID
	}
	return result
}
