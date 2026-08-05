package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

const shippingCookie = "odf_cart_shipping"

func (h Handler) addCartItem(w http.ResponseWriter, r *http.Request) {
	if !h.parseCommerceForm(w, r) {
		return
	}
	quantity, err := parsePositiveInt(r.FormValue("quantity"), 99)
	if err != nil {
		http.Error(w, "quantity must be between 1 and 99", http.StatusUnprocessableEntity)
		return
	}
	sku, err := h.resolvePostedSKU(r, r.FormValue("sku"), commerce.ProductTypeOneTime)
	if err != nil {
		h.renderCommerceInputError(w, r, err)
		return
	}
	addonIDs := make([]int64, 0, len(r.Form["addon_sku"]))
	for _, code := range r.Form["addon_sku"] {
		addon, loadErr := h.commerce.SKUByCode(r.Context(), code)
		if loadErr != nil || addon.ProductID != sku.ProductID || addon.Kind != commerce.SKUKindAddon {
			h.renderCommerceInputError(w, r, commerce.ErrInvalidInput)
			return
		}
		addonIDs = append(addonIDs, addon.ID)
	}
	var shippingOption *commerce.ShippingOption
	if rawShipping := strings.TrimSpace(r.FormValue("shipping_code")); rawShipping != "" {
		shippingID, parseErr := parsePositiveInt(rawShipping, 1<<62)
		if parseErr != nil {
			h.renderCommerceInputError(w, r, commerce.ErrInvalidInput)
			return
		}
		option, loadErr := h.commerce.ShippingOptionByID(r.Context(), shippingID)
		if loadErr != nil || !option.Active || option.ProductID != sku.ProductID {
			h.renderCommerceInputError(w, r, commerce.ErrInvalidInput)
			return
		}
		shippingOption = &option
	}
	cart, err := h.openCart(w, r)
	if err != nil {
		h.commerceError(w, "open cart", err)
		return
	}
	if _, err = h.commerce.AddCartSelection(r.Context(), cart.Token, commerce.CartSelectionParams{
		PrimarySKUID: sku.ID,
		AddonSKUIDs:  addonIDs,
		Quantity:     quantity,
	}); err != nil {
		h.renderCommerceInputError(w, r, err)
		return
	}
	if shippingOption != nil {
		selection := shippingSelections(r)
		selection[sku.ProductID] = shippingOption.ID
		h.setShippingSelections(w, selection)
	}
	http.Redirect(w, r, "/cart", http.StatusSeeOther)
}

func (h Handler) cartPage(w http.ResponseWriter, r *http.Request) {
	account := h.accountView(w, r)
	data := templates.CartPageData{
		Account: account, CheckoutAction: "/checkout", ContinueShoppingURL: "/services",
		CSRFToken: account.CSRFToken, ReferralToken: referralToken(r), Errors: map[string]string{},
	}
	if err := h.populateCartData(r, &data); err != nil && !errors.Is(err, commerce.ErrNotFound) {
		h.commerceError(w, "load cart", err)
		return
	}
	if user, ok := h.currentUser(r); ok {
		data.Name = user.DisplayName
		if strings.Contains(user.Username, "@") {
			data.Email = user.Username
		}
	}
	h.renderComponent(w, r, http.StatusOK, templates.CartPage(data))
}

func (h Handler) populateCartData(r *http.Request, data *templates.CartPageData) error {
	cart, err := h.cartFromRequest(r)
	if err != nil {
		return err
	}
	selections := shippingSelections(r)
	productNames := make(map[int64]string)
	var subtotalCents int64
	for _, item := range cart.Items {
		productName := productNames[item.SKU.ProductID]
		if productName == "" {
			if product, loadErr := h.commerce.ProductByID(r.Context(), item.SKU.ProductID); loadErr == nil {
				productName = product.Name
				productNames[item.SKU.ProductID] = productName
			}
		}
		lineTotal := item.SKU.PriceCents * item.Quantity
		subtotalCents += lineTotal
		data.Lines = append(data.Lines, templates.CartLineView{
			ID: strconv.FormatInt(item.SKU.ID, 10), ProductName: productName, SKU: item.SKU.SKU,
			SKUName: item.SKU.Name, OptionSummary: optionSummary(item.SKU.OptionsJSON), Quantity: int(item.Quantity),
			UnitPrice: money(item.SKU.PriceCents, "USD"), LineTotal: money(lineTotal, "USD"),
			UpdateAction: "/cart/items/update", RemoveAction: "/cart/items/remove",
		})
	}
	var shippingCents int64
	for productID := range productNames {
		if optionID := selections[productID]; optionID != 0 {
			if option, loadErr := h.commerce.ShippingOptionByID(r.Context(), optionID); loadErr == nil && option.Active && option.ProductID == productID {
				shippingCents += option.AmountCents
			}
		}
	}
	data.Subtotal = money(subtotalCents, "USD")
	data.Shipping = money(shippingCents, "USD")
	data.Total = money(subtotalCents+shippingCents, "USD")
	data.CanCheckout = len(cart.Items) > 0
	return nil
}

func (h Handler) updateCartItem(w http.ResponseWriter, r *http.Request) {
	if !h.parseCommerceForm(w, r) {
		return
	}
	skuID, idErr := parsePositiveInt(r.FormValue("line_id"), 1<<62)
	quantity, quantityErr := parsePositiveInt(r.FormValue("quantity"), 99)
	cart, cartErr := h.cartFromRequest(r)
	if idErr != nil || quantityErr != nil || cartErr != nil {
		http.Error(w, "invalid cart update", http.StatusUnprocessableEntity)
		return
	}
	if _, err := h.commerce.UpdateCartItem(r.Context(), cart.Token, skuID, quantity); err != nil {
		h.renderCommerceInputError(w, r, err)
		return
	}
	http.Redirect(w, r, "/cart", http.StatusSeeOther)
}

func (h Handler) removeCartItem(w http.ResponseWriter, r *http.Request) {
	if !h.parseCommerceForm(w, r) {
		return
	}
	skuID, idErr := parsePositiveInt(r.FormValue("line_id"), 1<<62)
	cart, cartErr := h.cartFromRequest(r)
	if idErr != nil || cartErr != nil {
		http.Error(w, "invalid cart update", http.StatusUnprocessableEntity)
		return
	}
	if _, err := h.commerce.RemoveCartItem(r.Context(), cart.Token, skuID); err != nil {
		h.renderCommerceInputError(w, r, err)
		return
	}
	http.Redirect(w, r, "/cart", http.StatusSeeOther)
}

func (h Handler) resolvePostedSKU(r *http.Request, code string, productType commerce.ProductType) (commerce.SKU, error) {
	sku, err := h.commerce.SKUByCode(r.Context(), strings.TrimSpace(code))
	if err == nil {
		product, productErr := h.commerce.ProductByID(r.Context(), sku.ProductID)
		if productErr != nil || product.Type != productType || !product.Active || !sku.Active || sku.Kind != commerce.SKUKindPrimary {
			return commerce.SKU{}, commerce.ErrInvalidInput
		}
		return sku, nil
	}
	product, productErr := h.commerce.ProductByMasterSKU(r.Context(), strings.TrimSpace(code))
	if productErr != nil || product.Type != productType || !product.Active {
		return commerce.SKU{}, commerce.ErrNotFound
	}
	var available []commerce.SKU
	for _, candidate := range product.SKUs {
		if candidate.Kind == commerce.SKUKindPrimary && candidate.Active && (candidate.Inventory == nil || *candidate.Inventory > 0) {
			available = append(available, candidate)
		}
	}
	if len(available) != 1 {
		return commerce.SKU{}, commerce.ErrInvalidInput
	}
	return available[0], nil
}

func (h Handler) openCart(w http.ResponseWriter, r *http.Request) (commerce.Cart, error) {
	if cart, err := h.cartFromRequest(r); err == nil && cart.Status == "open" {
		userID := currentUserID(h, r)
		referralID := h.referralID(r)
		if userID != nil {
			cart, _ = h.commerce.SetCartUser(r.Context(), cart.Token, userID)
		}
		if referralID != nil {
			cart, _ = h.commerce.SetCartReferral(r.Context(), cart.Token, referralID)
		}
		return cart, nil
	}
	cart, err := h.commerce.CreateCart(r.Context(), currentUserID(h, r), h.referralID(r))
	if err != nil {
		return commerce.Cart{}, err
	}
	http.SetCookie(w, &http.Cookie{Name: cartCookie, Value: cart.Token, Path: "/", MaxAge: int((30 * 24 * time.Hour).Seconds()), HttpOnly: true, Secure: h.config.SessionCookieSecure, SameSite: http.SameSiteLaxMode})
	return cart, nil
}

func (h Handler) cartFromRequest(r *http.Request) (commerce.Cart, error) {
	cookie, err := r.Cookie(cartCookie)
	if err != nil {
		return commerce.Cart{}, commerce.ErrNotFound
	}
	return h.commerce.CartByToken(r.Context(), cookie.Value)
}

func currentUserID(h Handler, r *http.Request) *int64 {
	if user, ok := h.currentUser(r); ok {
		return &user.ID
	}
	return nil
}

func (h Handler) parseCommerceForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxCommerceBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return false
	}
	if !h.validCSRF(r) {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return false
	}
	return true
}

func (h Handler) renderCommerceInputError(w http.ResponseWriter, _ *http.Request, err error) {
	status := http.StatusUnprocessableEntity
	message := "The selected product option is unavailable."
	if errors.Is(err, commerce.ErrSoldOut) {
		message = "That option is sold out."
	}
	http.Error(w, message, status)
}

func shippingSelections(r *http.Request) map[int64]int64 {
	result := make(map[int64]int64)
	cookie, err := r.Cookie(shippingCookie)
	if err != nil {
		return result
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return result
	}
	_ = json.Unmarshal(raw, &result)
	return result
}

func (h Handler) setShippingSelections(w http.ResponseWriter, selections map[int64]int64) {
	raw, err := json.Marshal(selections)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: shippingCookie, Value: base64.RawURLEncoding.EncodeToString(raw), Path: "/", MaxAge: int((30 * 24 * time.Hour).Seconds()), HttpOnly: true, Secure: h.config.SessionCookieSecure, SameSite: http.SameSiteLaxMode})
}
