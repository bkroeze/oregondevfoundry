package commerce

import (
	"errors"
	"time"
)

type ProductType string

const (
	ProductTypeOneTime      ProductType = "one_time"
	ProductTypeSubscription ProductType = "subscription"
)

type SKUKind string

const (
	SKUKindPrimary SKUKind = "primary"
	SKUKindAddon   SKUKind = "addon"
)

var (
	ErrNotFound              = errors.New("commerce record not found")
	ErrSoldOut               = errors.New("sku is sold out")
	ErrInvalidProductType    = errors.New("invalid product type")
	ErrSubscriptionInCart    = errors.New("subscription skus cannot be added to carts")
	ErrCartRequired          = errors.New("a one-time checkout requires a cart")
	ErrMixedProductShipping  = errors.New("mixed-product carts require per-product shipping selections")
	ErrStandaloneRequired    = errors.New("a subscription checkout requires a standalone sku")
	ErrInvalidInput          = errors.New("invalid commerce input")
	ErrInvitationAlreadyUsed = errors.New("invitation has already been used")
	ErrWebhookCompleted      = errors.New("webhook event has already been completed")
	ErrWebhookInProgress     = errors.New("webhook event is already in progress")
	ErrDeliveryInProgress    = errors.New("notification delivery is already in progress")
)

type Product struct {
	ID              int64
	Slug            string
	MasterSKU       string
	Name            string
	Description     string
	Type            ProductType
	Active          bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
	SKUs            []SKU
	Addons          []SKU
	ShippingOptions []ShippingOption
}

type SKU struct {
	ID            int64
	ProductID     int64
	SKU           string
	Name          string
	Kind          SKUKind
	OptionsJSON   string
	PriceCents    int64
	Inventory     *int64
	Active        bool
	StripePriceID string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type ShippingOption struct {
	ID          int64
	ProductID   int64
	Name        string
	AmountCents int64
	Active      bool
	SortOrder   int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ProductParams struct {
	Slug        string
	MasterSKU   string
	Name        string
	Description string
	Type        ProductType
	Active      bool
}

type SKUParams struct {
	ProductID     int64
	SKU           string
	Name          string
	Kind          SKUKind
	OptionsJSON   string
	PriceCents    int64
	Inventory     *int64
	Active        bool
	StripePriceID string
}

type ShippingOptionParams struct {
	ProductID   int64
	Name        string
	AmountCents int64
	Active      bool
	SortOrder   int
}

type CartSelectionParams struct {
	PrimarySKUID int64
	AddonSKUIDs  []int64
	Quantity     int64
}

type Cart struct {
	ID         int64
	Token      string
	UserID     *int64
	ReferralID *int64
	Status     string
	Items      []CartItem
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type CartItem struct {
	ID        int64
	CartID    int64
	SKU       SKU
	Quantity  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Checkout struct {
	ID                  int64
	StripeSessionID     string
	CartID              *int64
	StandaloneSKUID     *int64
	UserID              *int64
	InvitedUserID       *int64
	ReferralID          *int64
	BuyerEmail          string
	BuyerName           string
	CreateAccount       bool
	ShippingOptions     []CheckoutShipping
	ShippingOptionID    *int64
	ShippingName        string
	ShippingAmountCents int64
	Status              string
	Items               []CheckoutItem
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type CheckoutItem struct {
	ID             int64
	CheckoutID     int64
	ProductID      int64
	SKUID          int64
	ProductName    string
	SKU            string
	SKUName        string
	OptionsJSON    string
	Quantity       int64
	UnitPriceCents int64
	CreatedAt      time.Time
}

type CheckoutShipping struct {
	ID               int64
	CheckoutID       int64
	ProductID        int64
	ShippingOptionID int64
	ProductName      string
	Name             string
	AmountCents      int64
	CreatedAt        time.Time
}

type CreateCheckoutParams struct {
	CartToken         string
	StandaloneSKUID   int64
	StripeSessionID   string
	BuyerEmail        string
	BuyerName         string
	UserID            *int64
	CreateAccount     bool
	ShippingOptionID  *int64
	ShippingOptionIDs map[int64]int64
	ReferralID        *int64
}

type Order struct {
	ID                  int64
	CheckoutID          int64
	StripeSessionID     string
	UserID              *int64
	ReferralID          *int64
	BuyerEmail          string
	BuyerName           string
	ShippingName        string
	ShippingAmountCents int64
	SubtotalCents       int64
	TotalCents          int64
	EmailSentAt         *time.Time
	Items               []OrderItem
	CreatedAt           time.Time
}

type OrderItem struct {
	ID             int64
	OrderID        int64
	ProductID      int64
	SKUID          int64
	ProductName    string
	SKU            string
	SKUName        string
	OptionsJSON    string
	Quantity       int64
	UnitPriceCents int64
	CreatedAt      time.Time
}

type Subscription struct {
	ID                   int64
	CheckoutID           int64
	StripeSessionID      string
	StripeSubscriptionID string
	UserID               *int64
	ReferralID           *int64
	ProductID            int64
	SKUID                int64
	BuyerEmail           string
	BuyerName            string
	Status               string
	CurrentPeriodEnd     *time.Time
	EmailSentAt          *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type BookingConfirmation struct {
	ID             int64
	CheckoutID     int64
	OrderID        *int64
	SubscriptionID *int64
	Kind           ProductType
	Code           string
	EmailSentAt    *time.Time
	CreatedAt      time.Time
}

type FulfillmentResult struct {
	NewlyCreated    bool
	Order           *Order
	Subscription    *Subscription
	Confirmation    BookingConfirmation
	BuyerEmail      string
	BuyerName       string
	InvitationToken string
	InvitedUserID   *int64
	InvitationSent  bool
}

type FulfillParams struct {
	StripeSessionID      string
	StripeSubscriptionID string
	SubscriptionStatus   string
	CurrentPeriodEnd     *time.Time
}

type Referral struct {
	ID        int64
	Token     string
	SourceURL string
	Host      string
	CreatedAt time.Time
}

type Invitation struct {
	ID        int64
	UserID    int64
	Email     string
	UsedAt    *time.Time
	SentAt    *time.Time
	CreatedAt time.Time
}

type WebhookEvent struct {
	StripeEventID string
	EventType     string
	Status        string
	Attempts      int64
	LastError     string
	ClaimedAt     time.Time
	CompletedAt   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
