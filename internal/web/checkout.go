package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
	"github.com/bkroeze/oregon-dev-foundry/internal/notifications"
	"github.com/bkroeze/oregon-dev-foundry/internal/payment"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

func (h Handler) checkoutCart(w http.ResponseWriter, r *http.Request) {
	if !h.parseCommerceForm(w, r) {
		return
	}
	if h.payments == nil || h.config.PublicBaseURL == "" {
		http.Error(w, "checkout is not configured", http.StatusServiceUnavailable)
		return
	}
	name, email := strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("email"))
	if problems := validateBuyer(name, email); len(problems) != 0 {
		h.renderCartCheckoutError(w, r, name, email, r.FormValue("create_account") == "true", problems)
		return
	}
	cart, err := h.cartFromRequest(r)
	if err != nil || len(cart.Items) == 0 {
		http.Error(w, "your cart is empty", http.StatusUnprocessableEntity)
		return
	}
	if handled, resumeErr := h.resumeCartCheckout(w, r, cart); resumeErr != nil {
		h.commerceError(w, "resume Stripe checkout", resumeErr)
		return
	} else if handled {
		return
	}
	checkout, err := h.commerce.CreateCheckout(r.Context(), commerce.CreateCheckoutParams{
		CartToken: cart.Token, BuyerName: name, BuyerEmail: email, UserID: currentUserID(h, r),
		CreateAccount: r.FormValue("create_account") == "true", ShippingOptionIDs: shippingSelections(r), ReferralID: h.referralID(r),
	})
	if err != nil {
		h.renderCommerceInputError(w, r, err)
		return
	}
	h.startEmbeddedCheckout(w, r, checkout, payment.ModePayment)
}

func (h Handler) resumeCartCheckout(w http.ResponseWriter, r *http.Request, cart commerce.Cart) (bool, error) {
	checkout, err := h.commerce.CheckoutByCartID(r.Context(), cart.ID)
	if errors.Is(err, commerce.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if checkout.StripeSessionID == "" {
		if err := h.commerce.CancelPendingCheckout(r.Context(), checkout.ID); err != nil {
			return true, err
		}
		return false, nil
	}
	session, err := h.payments.GetSession(r.Context(), checkout.StripeSessionID)
	if err != nil {
		return true, err
	}
	switch session.Status {
	case "", "open":
		if session.ClientSecret == "" {
			return true, errors.New("Stripe checkout session has no client secret")
		}
		h.renderEmbeddedCheckout(w, r, checkout, session, checkoutTotal(checkout))
		return true, nil
	case "complete":
		http.Redirect(w, r, "/checkout/return?session_id="+url.QueryEscape(session.ID), http.StatusSeeOther)
		return true, nil
	case "expired":
		if err := h.commerce.CancelExpiredCheckout(r.Context(), checkout.ID, session.ID); err != nil {
			return true, err
		}
		return false, nil
	default:
		return true, errors.New("Stripe checkout session is not resumable")
	}
}

func (h Handler) checkoutSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.parseCommerceForm(w, r) {
		return
	}
	if h.payments == nil || h.config.PublicBaseURL == "" {
		http.Error(w, "checkout is not configured", http.StatusServiceUnavailable)
		return
	}
	name, email := strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("email"))
	problems := validateBuyer(name, email)
	sku, err := h.resolvePostedSKU(r, r.FormValue("sku"), commerce.ProductTypeSubscription)
	if err != nil {
		problems["sku"] = "Select an available monthly plan."
	}
	if len(problems) != 0 {
		product, loadErr := h.commerce.FindProduct(r.Context(), r.FormValue("sku"))
		if loadErr != nil && sku.ProductID != 0 {
			product, loadErr = h.commerce.ProductByID(r.Context(), sku.ProductID)
		}
		if loadErr != nil {
			h.renderCommerceInputError(w, r, err)
			return
		}
		account := h.accountView(w, r)
		h.renderComponent(w, r, http.StatusUnprocessableEntity, templates.ProductPage(templates.ProductPageData{
			Account: account, Product: productView(product, false), Action: "/subscriptions/checkout", CSRFToken: account.CSRFToken,
			ReferralToken: referralToken(r), Name: name, Email: email, CreateAccount: r.FormValue("create_account") == "true", Errors: problems,
		}))
		return
	}
	checkout, err := h.commerce.CreateCheckout(r.Context(), commerce.CreateCheckoutParams{
		StandaloneSKUID: sku.ID, BuyerName: name, BuyerEmail: email, UserID: currentUserID(h, r),
		CreateAccount: r.FormValue("create_account") == "true", ReferralID: h.referralID(r),
	})
	if err != nil {
		h.renderCommerceInputError(w, r, err)
		return
	}
	h.startEmbeddedCheckout(w, r, checkout, payment.ModeSubscription)
}

func (h Handler) startEmbeddedCheckout(w http.ResponseWriter, r *http.Request, checkout commerce.Checkout, mode string) {
	lines := make([]payment.Line, 0, len(checkout.Items)+len(checkout.ShippingOptions))
	var total int64
	for _, item := range checkout.Items {
		lines = append(lines, payment.Line{Name: item.ProductName + " — " + item.SKUName, Description: optionSummary(item.OptionsJSON), UnitCents: item.UnitPriceCents, Quantity: item.Quantity, Recurring: mode == payment.ModeSubscription})
		total += item.UnitPriceCents * item.Quantity
	}
	for _, shipping := range checkout.ShippingOptions {
		lines = append(lines, payment.Line{Name: "Shipping — " + shipping.ProductName, Description: shipping.Name, UnitCents: shipping.AmountCents, Quantity: 1})
		total += shipping.AmountCents
	}
	returnURL := h.config.PublicBaseURL + "/checkout/return?session_id={CHECKOUT_SESSION_ID}"
	session, err := h.payments.CreateSession(r.Context(), payment.CreateSessionParams{
		Mode: mode, Currency: "usd", CustomerEmail: checkout.BuyerEmail, ClientReference: strconv.FormatInt(checkout.ID, 10),
		ReturnURL: returnURL, Metadata: map[string]string{"checkout_id": strconv.FormatInt(checkout.ID, 10), "purchase_type": mode}, Lines: lines,
	})
	if err != nil {
		h.cancelUnattachedCheckout(r.Context(), checkout.ID)
		h.commerceError(w, "create Stripe checkout", err)
		return
	}
	if _, err = h.commerce.AttachStripeSession(r.Context(), checkout.ID, session.ID); err != nil {
		cleanupErr := h.expireUnattachedStripeSession(r.Context(), checkout.ID, session.ID)
		h.commerceError(w, "attach Stripe checkout", errors.Join(err, cleanupErr))
		return
	}
	h.renderEmbeddedCheckout(w, r, checkout, session, total)
}

func (h Handler) expireUnattachedStripeSession(parent context.Context, checkoutID int64, sessionID string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	if err := h.payments.ExpireSession(cleanupCtx, sessionID); err != nil {
		return err
	}
	if err := h.commerce.CancelExpiredCheckout(cleanupCtx, checkoutID, sessionID); err != nil && !errors.Is(err, commerce.ErrNotFound) {
		return err
	}
	return nil
}

func (h Handler) cancelUnattachedCheckout(parent context.Context, checkoutID int64) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	if err := h.commerce.CancelPendingCheckout(cleanupCtx, checkoutID); err != nil && !errors.Is(err, commerce.ErrNotFound) {
		slog.Error("cancel unattached Stripe checkout", "checkout_id", checkoutID, "error", err)
	}
}

func checkoutTotal(checkout commerce.Checkout) int64 {
	var total int64
	for _, item := range checkout.Items {
		total += item.UnitPriceCents * item.Quantity
	}
	for _, shipping := range checkout.ShippingOptions {
		total += shipping.AmountCents
	}
	return total
}

func (h Handler) renderEmbeddedCheckout(w http.ResponseWriter, r *http.Request, checkout commerce.Checkout, session payment.Session, total int64) {
	h.renderComponent(w, r, http.StatusOK, templates.EmbeddedCheckoutPage(templates.EmbeddedCheckoutData{
		Account: h.accountView(w, r), PublishableKey: h.config.StripePublishableKey, ClientSecret: session.ClientSecret,
		Total: money(total, "USD"), Email: checkout.BuyerEmail,
	}))
}

func (h Handler) checkoutReturn(w http.ResponseWriter, r *http.Request) {
	if h.payments == nil {
		http.Error(w, "checkout is not configured", http.StatusServiceUnavailable)
		return
	}
	session, err := h.payments.GetSession(r.Context(), r.URL.Query().Get("session_id"))
	if err != nil {
		h.commerceError(w, "retrieve Stripe checkout", err)
		return
	}
	if session.PaymentStatus != "paid" && session.PaymentStatus != "no_payment_required" {
		h.renderComponent(w, r, http.StatusAccepted, templates.ConfirmationPage(templates.ConfirmationData{
			Account: h.accountView(w, r), Email: session.CustomerEmail, Status: "Payment processing", Message: "We will send confirmation after Stripe reports payment complete.",
		}))
		return
	}
	result, checkout, err := h.fulfillSession(r.Context(), session)
	if err != nil {
		h.commerceError(w, "fulfill Stripe checkout", err)
		return
	}
	if err := h.deliverFulfillment(r.Context(), result, checkout); err != nil {
		slog.Error("deliver purchase confirmation", "error", err)
	}
	h.renderComponent(w, r, http.StatusOK, templates.ConfirmationPage(h.confirmationData(w, r, result, checkout)))
}

func (h Handler) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	if h.payments == nil {
		http.Error(w, "checkout is not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "invalid webhook", http.StatusBadRequest)
		return
	}
	event, err := h.payments.ParseWebhook(body, r.Header.Get("Stripe-Signature"))
	if err != nil {
		http.Error(w, "invalid webhook", http.StatusBadRequest)
		return
	}
	if event.Type != "checkout.session.completed" && event.Type != "checkout.session.async_payment_succeeded" && event.Type != "checkout.session.async_payment_failed" && event.Type != "checkout.session.expired" {
		w.WriteHeader(http.StatusOK)
		return
	}
	claimed, err := h.commerce.ClaimWebhookEvent(r.Context(), event.ID, event.Type)
	if err != nil {
		h.commerceError(w, "claim Stripe webhook", err)
		return
	}
	if !claimed {
		w.WriteHeader(http.StatusOK)
		return
	}
	if event.Type == "checkout.session.expired" || event.Type == "checkout.session.async_payment_failed" {
		checkout, processErr := h.commerce.CheckoutByStripeSession(r.Context(), event.Session.ID)
		if errors.Is(processErr, commerce.ErrNotFound) {
			processErr = nil
		} else if processErr == nil {
			processErr = h.commerce.CancelExpiredCheckout(r.Context(), checkout.ID, event.Session.ID)
			if errors.Is(processErr, commerce.ErrNotFound) {
				processErr = nil
			}
		}
		if processErr != nil {
			_ = h.commerce.ReleaseWebhookEvent(r.Context(), event.ID, processErr)
			h.commerceError(w, "release failed Stripe checkout", processErr)
			return
		}
		if err := h.commerce.CompleteWebhookEvent(r.Context(), event.ID); err != nil {
			h.commerceError(w, "complete failed Stripe webhook", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	if event.Session.PaymentStatus != "paid" && event.Session.PaymentStatus != "no_payment_required" {
		_ = h.commerce.CompleteWebhookEvent(r.Context(), event.ID)
		w.WriteHeader(http.StatusOK)
		return
	}
	result, checkout, processErr := h.fulfillSession(r.Context(), event.Session)
	if processErr == nil {
		processErr = h.deliverFulfillment(r.Context(), result, checkout)
	}
	if processErr != nil {
		_ = h.commerce.ReleaseWebhookEvent(r.Context(), event.ID, processErr)
		h.commerceError(w, "process Stripe webhook", processErr)
		return
	}
	if err := h.commerce.CompleteWebhookEvent(r.Context(), event.ID); err != nil {
		h.commerceError(w, "complete Stripe webhook", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h Handler) fulfillSession(ctx context.Context, session payment.Session) (commerce.FulfillmentResult, commerce.Checkout, error) {
	checkout, err := h.commerce.CheckoutByStripeSession(ctx, session.ID)
	if err != nil {
		return commerce.FulfillmentResult{}, commerce.Checkout{}, err
	}
	if rawID := session.Metadata["checkout_id"]; rawID != "" && rawID != strconv.FormatInt(checkout.ID, 10) {
		return commerce.FulfillmentResult{}, commerce.Checkout{}, errors.New("Stripe checkout metadata does not match checkout")
	}
	result, err := h.commerce.Fulfill(ctx, commerce.FulfillParams{
		StripeSessionID: session.ID, StripeSubscriptionID: session.SubscriptionID, SubscriptionStatus: "active",
	})
	return result, checkout, err
}

func (h Handler) deliverFulfillment(ctx context.Context, result commerce.FulfillmentResult, checkout commerce.Checkout) error {
	if h.mailer == nil {
		return errors.New("order mailer is not configured")
	}
	if result.Order != nil {
		claimed, err := h.commerce.ClaimOrderEmail(ctx, result.Order.ID)
		if err != nil {
			return err
		}
		if claimed {
			receipt := notifications.OrderStatus{To: result.Order.BuyerEmail, PurchaserName: result.Order.BuyerName, ConfirmationNumber: result.Confirmation.Code, Status: "confirmed", PurchaseType: "order", Currency: "USD", SubtotalCents: result.Order.SubtotalCents, ShippingCents: result.Order.ShippingAmountCents, TotalCents: result.Order.TotalCents}
			for _, item := range result.Order.Items {
				receipt.Lines = append(receipt.Lines, notifications.ReceiptLine{SKU: item.SKU, Name: item.ProductName + " — " + item.SKUName, Quantity: item.Quantity, UnitCents: item.UnitPriceCents, LineCents: item.UnitPriceCents * item.Quantity})
			}
			if err := h.mailer.SendOrderStatus(ctx, receipt); err != nil {
				_ = h.commerce.ReleaseOrderEmail(ctx, result.Order.ID)
				return err
			}
			if err := h.commerce.MarkOrderEmailSent(ctx, result.Order.ID); err != nil {
				return err
			}
		}
	}
	if result.Subscription != nil {
		claimed, err := h.commerce.ClaimSubscriptionEmail(ctx, result.Subscription.ID)
		if err != nil {
			return err
		}
		if claimed {
			item := checkout.Items[0]
			receipt := notifications.OrderStatus{To: result.Subscription.BuyerEmail, PurchaserName: result.Subscription.BuyerName, ConfirmationNumber: result.Confirmation.Code, Status: result.Subscription.Status, PurchaseType: "subscription", Currency: "USD", SubtotalCents: item.UnitPriceCents, TotalCents: item.UnitPriceCents, Lines: []notifications.ReceiptLine{{SKU: item.SKU, Name: item.ProductName + " — " + item.SKUName, Quantity: 1, UnitCents: item.UnitPriceCents, LineCents: item.UnitPriceCents}}}
			if err := h.mailer.SendOrderStatus(ctx, receipt); err != nil {
				_ = h.commerce.ReleaseSubscriptionEmail(ctx, result.Subscription.ID)
				return err
			}
			if err := h.commerce.MarkSubscriptionEmailSent(ctx, result.Subscription.ID); err != nil {
				return err
			}
		}
	}
	if result.InvitedUserID != nil && !result.InvitationSent {
		claimed, err := h.commerce.ClaimInvitationDelivery(ctx, *result.InvitedUserID)
		if err != nil {
			return err
		}
		if claimed {
			token := result.InvitationToken
			if token == "" {
				token, err = h.commerce.IssueClaimedInvitationToken(ctx, *result.InvitedUserID)
				if err != nil {
					return err
				}
			}
			activationURL := h.config.PublicBaseURL + "/account/invite?token=" + url.QueryEscape(token)
			if err := h.mailer.SendInvitation(ctx, notifications.Invitation{To: result.BuyerEmail, PurchaserName: result.BuyerName, ActivationURL: activationURL}); err != nil {
				_ = h.commerce.ReleaseInvitationDelivery(ctx, *result.InvitedUserID)
				return err
			}
			if err := h.commerce.MarkInvitationSent(ctx, *result.InvitedUserID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h Handler) confirmationData(w http.ResponseWriter, r *http.Request, result commerce.FulfillmentResult, checkout commerce.Checkout) templates.ConfirmationData {
	data := templates.ConfirmationData{Account: h.accountView(w, r), OrderNumber: result.Confirmation.Code, Email: result.BuyerEmail, Status: "Confirmed", Message: "Payment received. Your confirmation has been created."}
	for _, item := range checkout.Items {
		lineTotal := item.UnitPriceCents * item.Quantity
		data.Lines = append(data.Lines, templates.CartLineView{ProductName: item.ProductName, SKU: item.SKU, SKUName: item.SKUName, OptionSummary: optionSummary(item.OptionsJSON), Quantity: int(item.Quantity), UnitPrice: money(item.UnitPriceCents, "USD"), LineTotal: money(lineTotal, "USD")})
	}
	var subtotal, shipping int64
	for _, item := range checkout.Items {
		subtotal += item.UnitPriceCents * item.Quantity
	}
	for _, option := range checkout.ShippingOptions {
		shipping += option.AmountCents
	}
	data.Subtotal, data.Shipping, data.Total = money(subtotal, "USD"), money(shipping, "USD"), money(subtotal+shipping, "USD")
	return data
}

func (h Handler) renderCartCheckoutError(w http.ResponseWriter, r *http.Request, name, email string, create bool, problems map[string]string) {
	account := h.accountView(w, r)
	data := templates.CartPageData{
		Account: account, Name: name, Email: email, CreateAccount: create,
		CheckoutAction: "/checkout", ContinueShoppingURL: "/services", CSRFToken: account.CSRFToken,
		ReferralToken: referralToken(r), Errors: problems, Error: "Check the purchaser details and try again.",
	}
	if err := h.populateCartData(r, &data); err != nil {
		h.commerceError(w, "reload cart after checkout validation", err)
		return
	}
	h.renderComponent(w, r, http.StatusUnprocessableEntity, templates.CartPage(data))
}
