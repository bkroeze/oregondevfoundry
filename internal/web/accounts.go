package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

func (h Handler) passwordChangePage(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAccount(w, r)
	if !ok {
		return
	}
	data := templates.PasswordChangeData{Account: account, Errors: map[string]string{}}
	status := http.StatusOK
	if account.Role == string(auth.RoleAdmin) {
		status = http.StatusForbidden
		data.Error = "Administrator passwords can only be changed with the user management command-line tool."
	}
	h.renderComponent(w, r, status, templates.PasswordChangePage(data))
}

func (h Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAccount(w, r)
	if !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	data := templates.PasswordChangeData{Account: account, Errors: map[string]string{}}
	if account.Role == string(auth.RoleAdmin) {
		data.Error = "Administrator passwords can only be changed with the user management command-line tool."
		h.renderComponent(w, r, http.StatusForbidden, templates.PasswordChangePage(data))
		return
	}
	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	if currentPassword == "" {
		data.Errors["current_password"] = "Enter your current password."
	}
	if newPassword == "" {
		data.Errors["new_password"] = "Enter a new password."
	}
	if newPassword != r.FormValue("confirm_password") {
		data.Errors["confirm_password"] = "The new passwords do not match."
	}
	if len(data.Errors) != 0 {
		h.renderComponent(w, r, http.StatusUnprocessableEntity, templates.PasswordChangePage(data))
		return
	}
	user, ok := h.currentUser(r)
	if !ok {
		h.expireCookie(w, sessionCookie)
		h.expireCookie(w, csrfCookie)
		http.Redirect(w, r, "/login?next=/profile/password", http.StatusSeeOther)
		return
	}
	if err := h.users.ChangePassword(r.Context(), user.ID, []byte(currentPassword), []byte(newPassword)); err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidCredential):
			data.Errors["current_password"] = "The current password is incorrect."
			h.renderComponent(w, r, http.StatusUnprocessableEntity, templates.PasswordChangePage(data))
		case errors.Is(err, auth.ErrAdminPasswordChange):
			data.Error = "Administrator passwords can only be changed with the user management command-line tool."
			h.renderComponent(w, r, http.StatusForbidden, templates.PasswordChangePage(data))
		default:
			slog.Error("change account password", "user_id", user.ID, "error", err)
			data.Error = "The password could not be changed. Try again."
			h.renderComponent(w, r, http.StatusInternalServerError, templates.PasswordChangePage(data))
		}
		return
	}
	h.expireCookie(w, sessionCookie)
	h.expireCookie(w, csrfCookie)
	http.Redirect(w, r, "/login?status="+url.QueryEscape("Password changed. Please log in again."), http.StatusSeeOther)
}

func (h Handler) deleteAccountPage(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAccount(w, r)
	if !ok {
		return
	}
	h.renderComponent(w, r, http.StatusOK, templates.DeleteAccountPage(templates.DeleteAccountData{Account: account, Errors: map[string]string{}}))
}

func (h Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAccount(w, r)
	if !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	data := templates.DeleteAccountData{Account: account, Errors: map[string]string{}}
	password := r.FormValue("current_password")
	if password == "" {
		data.Errors["current_password"] = "Enter your current password."
	}
	if r.FormValue("confirm_delete") != "true" {
		data.Errors["confirm_delete"] = "Confirm that you understand the retention and cancellation terms."
	}
	if len(data.Errors) != 0 {
		h.renderComponent(w, r, http.StatusUnprocessableEntity, templates.DeleteAccountPage(data))
		return
	}
	user, ok := h.currentUser(r)
	if !ok {
		h.expireCookie(w, sessionCookie)
		h.expireCookie(w, csrfCookie)
		http.Redirect(w, r, "/login?next=/profile/delete", http.StatusSeeOther)
		return
	}
	if err := h.users.VerifyPassword(r.Context(), user.ID, []byte(password)); err != nil {
		if errors.Is(err, auth.ErrInvalidCredential) {
			data.Errors["current_password"] = "The current password is incorrect."
			h.renderComponent(w, r, http.StatusUnprocessableEntity, templates.DeleteAccountPage(data))
			return
		}
		slog.Error("verify account deletion password", "user_id", user.ID, "error", err)
		data.Error = "The account could not be deleted. Try again."
		h.renderComponent(w, r, http.StatusInternalServerError, templates.DeleteAccountPage(data))
		return
	}
	if err := h.cancelAndDeleteUser(r, user.ID); err != nil {
		slog.Error("delete account", "user_id", user.ID, "error", err)
		data.Error = "We could not cancel every open checkout or subscription, so your account was not deleted. Try again or contact support."
		h.renderComponent(w, r, http.StatusBadGateway, templates.DeleteAccountPage(data))
		return
	}
	h.expireCookie(w, sessionCookie)
	h.expireCookie(w, csrfCookie)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h Handler) adminUsers(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAdminCommerce(w, r)
	if !ok {
		return
	}
	users, err := h.users.ListUsers(r.Context())
	if err != nil {
		slog.Error("list admin users", "error", err)
		h.renderComponent(w, r, http.StatusInternalServerError, templates.AdminUsersPage(templates.AdminUsersData{Account: account, NewURL: "/admin/users/new", Error: "Users could not be loaded."}))
		return
	}
	views := make([]templates.AdminUserView, 0, len(users))
	for _, user := range users {
		views = append(views, basicAdminUserView(user))
	}
	h.renderComponent(w, r, http.StatusOK, templates.AdminUsersPage(templates.AdminUsersData{Account: account, Users: views, NewURL: "/admin/users/new", Status: r.URL.Query().Get("status")}))
}

func (h Handler) newAdminUser(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAdminCommerce(w, r)
	if !ok {
		return
	}
	data := templates.AdminUserData{
		Account: account, User: basicAdminUserView(auth.User{Role: auth.RoleUser}), IsNew: true,
		SaveAction: "/admin/users", CSRFToken: account.CSRFToken, Errors: map[string]string{},
	}
	h.renderComponent(w, r, http.StatusOK, templates.AdminUserPage(data))
}

func (h Handler) createAdminUser(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAdminCommerce(w, r)
	if !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	params := auth.CreateUserParams{
		Username: r.FormValue("username"), Email: r.FormValue("email"), DisplayName: r.FormValue("display_name"),
		Role: auth.Role(r.FormValue("role")), HasPurchases: r.FormValue("has_purchases") == "true", Password: []byte(r.FormValue("password")),
	}
	user, err := h.users.CreateUser(r.Context(), params)
	if err != nil {
		data := templates.AdminUserData{Account: account, User: adminUserViewFromCreate(params), IsNew: true, SaveAction: "/admin/users", CSRFToken: account.CSRFToken, Errors: map[string]string{}}
		h.renderAdminUserError(w, r, data, err)
		return
	}
	http.Redirect(w, r, "/admin/users/"+strconv.FormatInt(user.ID, 10)+"?status="+url.QueryEscape("User created"), http.StatusSeeOther)
}

func (h Handler) adminUser(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAdminCommerce(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := h.adminUserData(r, account, id)
	if errors.Is(err, auth.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.Error("load admin user", "user_id", id, "error", err)
		http.Error(w, "user could not be loaded", http.StatusInternalServerError)
		return
	}
	data.Status = r.URL.Query().Get("status")
	h.renderComponent(w, r, http.StatusOK, templates.AdminUserPage(data))
}

func (h Handler) updateAdminUser(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAdminCommerce(w, r)
	if !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	version, err := parsePositiveInt(r.FormValue("version"), 1<<62)
	if err != nil {
		http.Error(w, "invalid user version", http.StatusUnprocessableEntity)
		return
	}
	params := auth.UpdateUserParams{
		Username: r.FormValue("username"), Email: r.FormValue("email"), DisplayName: r.FormValue("display_name"),
		Role: auth.Role(r.FormValue("role")), HasPurchases: r.FormValue("has_purchases") == "true", Password: []byte(r.FormValue("password")),
		ExpectedVersion: version, ProtectAdminPassword: true,
	}
	if _, err = h.users.UpdateUser(r.Context(), id, params); err != nil {
		data, loadErr := h.adminUserData(r, account, id)
		if loadErr != nil {
			slog.Error("reload admin user after update error", "user_id", id, "error", loadErr)
			http.Error(w, "user could not be updated", http.StatusInternalServerError)
			return
		}
		data.User.Username = strings.TrimSpace(params.Username)
		data.User.Email = strings.TrimSpace(params.Email)
		data.User.DisplayName = strings.TrimSpace(params.DisplayName)
		data.User.Role = string(params.Role)
		data.User.HasPurchases = params.HasPurchases
		h.renderAdminUserError(w, r, data, err)
		return
	}
	http.Redirect(w, r, "/admin/users/"+strconv.FormatInt(id, 10)+"?status="+url.QueryEscape("User saved"), http.StatusSeeOther)
}

func (h Handler) deleteAdminUser(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAdminCommerce(w, r)
	if !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.users.UserByID(r.Context(), id); errors.Is(err, auth.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		slog.Error("load user for admin deletion", "user_id", id, "error", err)
		http.Error(w, "user could not be loaded", http.StatusInternalServerError)
		return
	}
	current, currentOK := h.currentUser(r)
	if !currentOK || current.ID == id {
		data, loadErr := h.adminUserData(r, account, id)
		if loadErr != nil {
			http.Error(w, "user could not be deleted", http.StatusConflict)
			return
		}
		data.Error = "Use the Delete my account workflow to delete your own account."
		h.renderComponent(w, r, http.StatusConflict, templates.AdminUserPage(data))
		return
	}
	if err := h.cancelAndDeleteUser(r, id); err != nil {
		slog.Error("admin delete user", "user_id", id, "error", err)
		data, loadErr := h.adminUserData(r, account, id)
		if loadErr != nil {
			http.Error(w, "user could not be deleted", http.StatusBadGateway)
			return
		}
		data.Error = "The user was not deleted because an open checkout or subscription could not be canceled."
		h.renderComponent(w, r, http.StatusBadGateway, templates.AdminUserPage(data))
		return
	}
	http.Redirect(w, r, "/admin/users?status="+url.QueryEscape("User deleted"), http.StatusSeeOther)
}

func (h Handler) cancelAndDeleteUser(r *http.Request, userID int64) error {
	for range 3 {
		targets, err := h.commerce.CancellationTargetsForUser(r.Context(), userID)
		if err != nil {
			return err
		}
		if (len(targets.CheckoutSessionIDs) != 0 || len(targets.SubscriptionIDs) != 0) && h.payments == nil {
			return errors.New("payment gateway is not configured")
		}
		for _, sessionID := range targets.CheckoutSessionIDs {
			if err := h.payments.ExpireSession(r.Context(), sessionID); err != nil {
				return fmt.Errorf("expire checkout %s: %w", sessionID, err)
			}
			if err := h.commerce.CancelExpiredCheckoutForUser(r.Context(), userID, sessionID); err != nil {
				return err
			}
		}
		for _, subscriptionID := range targets.SubscriptionIDs {
			if err := h.payments.CancelSubscription(r.Context(), subscriptionID); err != nil {
				return fmt.Errorf("cancel subscription %s: %w", subscriptionID, err)
			}
			if err := h.commerce.MarkSubscriptionCanceledForUser(r.Context(), userID, subscriptionID); err != nil {
				return err
			}
		}
		if err := h.commerce.AbandonOpenCartsForUser(r.Context(), userID); err != nil {
			return err
		}
		if err := h.commerce.FinalizeUserDeletion(r.Context(), userID); err != nil {
			if errors.Is(err, commerce.ErrCancellationRequired) {
				continue
			}
			return err
		}
		return nil
	}
	return commerce.ErrCancellationRequired
}

func (h Handler) adminUserData(r *http.Request, account templates.AccountView, id int64) (templates.AdminUserData, error) {
	user, err := h.users.UserByID(r.Context(), id)
	if err != nil {
		return templates.AdminUserData{}, err
	}
	history, err := h.commerce.PurchaseHistoryForUser(r.Context(), id)
	if err != nil {
		return templates.AdminUserData{}, err
	}
	view := basicAdminUserView(user)
	view.Orders = adminOrderHistoryViews(history)
	view.Subscriptions = adminSubscriptionHistoryViews(history)
	base := "/admin/users/" + strconv.FormatInt(id, 10)
	return templates.AdminUserData{Account: account, User: view, SaveAction: base, DeleteAction: base + "/delete", CSRFToken: account.CSRFToken, Errors: map[string]string{}}, nil
}

func basicAdminUserView(user auth.User) templates.AdminUserView {
	view := templates.AdminUserView{
		ID: strconv.FormatInt(user.ID, 10), Username: user.Username, Email: user.Email, DisplayName: user.DisplayName,
		Role: string(user.Role), Status: string(user.CustomerStatus()), Version: user.Version,
		HasPurchases: user.HasPurchases, IsAdmin: user.Role == auth.RoleAdmin,
	}
	if !user.CreatedAt.IsZero() {
		view.CreatedAt = user.CreatedAt.UTC().Format("Jan 2, 2006 at 15:04 UTC")
	}
	if !user.UpdatedAt.IsZero() {
		view.UpdatedAt = user.UpdatedAt.UTC().Format("Jan 2, 2006 at 15:04 UTC")
	}
	return view
}

func adminUserViewFromCreate(params auth.CreateUserParams) templates.AdminUserView {
	return templates.AdminUserView{Username: strings.TrimSpace(params.Username), Email: strings.TrimSpace(params.Email), DisplayName: strings.TrimSpace(params.DisplayName), Role: string(params.Role), HasPurchases: params.HasPurchases, IsAdmin: params.Role == auth.RoleAdmin}
}

func adminOrderHistoryViews(history commerce.UserPurchaseHistory) []templates.AdminOrderView {
	views := make([]templates.AdminOrderView, 0, len(history.Orders))
	for _, entry := range history.Orders {
		summary := make([]string, 0, len(entry.Order.Items))
		for _, item := range entry.Order.Items {
			name := item.ProductName
			if item.SKUName != "" {
				name += " — " + item.SKUName
			}
			summary = append(summary, strconv.FormatInt(item.Quantity, 10)+" × "+name)
		}
		views = append(views, templates.AdminOrderView{
			ID: strconv.FormatInt(entry.Order.ID, 10), Confirmation: entry.ConfirmationCode,
			Status: "Paid", Total: string(money(entry.Order.TotalCents, "USD")), Summary: strings.Join(summary, ", "),
			CreatedAt: entry.Order.CreatedAt.UTC().Format("Jan 2, 2006 at 15:04 UTC"),
		})
	}
	return views
}

func adminSubscriptionHistoryViews(history commerce.UserPurchaseHistory) []templates.AdminSubscriptionView {
	views := make([]templates.AdminSubscriptionView, 0, len(history.Subscriptions))
	for _, entry := range history.Subscriptions {
		summary := entry.ProductName
		if entry.SKUName != "" {
			summary += " — " + entry.SKUName
		}
		views = append(views, templates.AdminSubscriptionView{
			ID: strconv.FormatInt(entry.Subscription.ID, 10), StripeID: entry.Subscription.StripeSubscriptionID,
			Status: entry.Subscription.Status, Summary: summary,
			CreatedAt: entry.Subscription.CreatedAt.UTC().Format("Jan 2, 2006 at 15:04 UTC"),
		})
	}
	return views
}

func (h Handler) renderAdminUserError(w http.ResponseWriter, r *http.Request, data templates.AdminUserData, err error) {
	status := http.StatusUnprocessableEntity
	switch {
	case errors.Is(err, auth.ErrUsernameExists):
		data.Errors["username"] = "That username is already in use."
	case errors.Is(err, auth.ErrEmailExists):
		data.Errors["email"] = "That email address is already in use."
	case errors.Is(err, auth.ErrConcurrentUpdate):
		status = http.StatusConflict
		data.Error = "This user changed while you were editing. Reload and try again."
	case errors.Is(err, auth.ErrAdminPasswordChange):
		data.Errors["password"] = "Administrator passwords can only be changed with the user management command-line tool."
	default:
		data.Error = "Check the user details and try again."
	}
	h.renderComponent(w, r, status, templates.AdminUserPage(data))
}
