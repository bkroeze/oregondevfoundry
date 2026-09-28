# Admin catalog

Administrators manage products, primary and add-on SKUs, prices, finite or unlimited inventory, and fixed shipping options that immediately determine the public purchase surfaces.

## User entry points

- `GET /admin/shop` aliases the admin commerce landing page.
- `GET /admin/products`, `GET /admin/products/new`, and `GET /admin/products/{id}` show catalog management surfaces.
- `POST /admin/products...` creates or updates products, SKUs, add-ons, inventory, pricing, and shipping options; dedicated `/delete` posts deactivate SKUs and shipping options.

## Primary source path

- **Entry:** `internal/web/admin_commerce.go::Handler.adminProducts` — loads the admin catalog and routes agents to the colocated product, SKU, and shipping handlers.
- **Effect boundary:** `internal/commerce/catalog.go::Store.UpdateSKU` — representative durable catalog mutation; adjacent `Store` methods in the same file own products, add-ons, and shipping options.

## Variants and gates

- Every surface requires an authenticated admin; non-admin users cannot use product-management routes.
- Every mutation requires a valid CSRF form.
- Primary SKUs are sellable variants; add-on SKUs attach only to eligible one-time primary selections.
- `inventory_count = NULL` means unlimited stock; a non-negative integer is finite inventory.
- Product type chooses one-time cart checkout versus direct subscription checkout.

## Effects and invariants

- Active product and SKU state controls public catalog visibility without deleting historical checkout or order data.
- SKU price and inventory changes affect public rendering and subsequent cart validation.
- SKU and shipping deletion handlers deactivate records rather than physically deleting them.
- Inactive shipping remains visible to administrators but is absent from public product views.

## Verification

- **Focused check:** `go test ./internal/web -run '^(TestAdminProductManagementRequiresAdmin|TestProductViewHidesInactiveShippingFromPublic)$'`
- **Runtime path:** sign in as a disposable admin, create a one-time product and primary SKU, set finite inventory and shipping, inspect the public product, then deactivate the shipping option.
- **Evidence:** a non-admin receives access denied; the admin changes persist; public price, stock, and shipping reflect active catalog state while inactive records remain visible in admin history.

## Gotchas

- Use authored `internal/templates/commerce.templ`, not generated `commerce_templ.go`, for UI changes.
- `requireAdminCommerce` is a shared gate, not the persistence layer.
- `UpdateSKU` is a routing seed, not an exhaustive effect list; product and shipping mutations are adjacent methods in `catalog.go`.
- Admin user CRUD and password policy are separate from commerce catalog management.
