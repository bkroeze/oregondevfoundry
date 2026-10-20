const menuButton = document.querySelector('.menu-button');
const nav = document.querySelector('#site-nav');

menuButton?.addEventListener('click', () => {
  const open = menuButton.getAttribute('aria-expanded') !== 'true';
  menuButton.setAttribute('aria-expanded', String(open));
  menuButton.textContent = open ? 'Close' : 'Menu';
  nav.classList.toggle('open', open);
});

nav?.addEventListener('click', (event) => {
  if (!event.target.closest('a')) return;
  menuButton?.setAttribute('aria-expanded', 'false');
  if (menuButton) menuButton.textContent = 'Menu';
  nav.classList.remove('open');
});

const year = document.querySelector('[data-year]');
if (year) year.textContent = new Date().getFullYear();

document.body.addEventListener('htmx:afterSwap', (event) => {
  const target = event.detail.target;
  if (target.id === 'contact-form' && window.turnstile) window.turnstile.render(target.querySelector('.cf-turnstile'));
});

const formatCommerceMoney = (cents, currency) => {
  const amount = Number.isFinite(cents) ? cents / 100 : 0;
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency: (currency || 'USD').toUpperCase(),
    }).format(amount);
  } catch {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency: 'USD',
    }).format(amount);
  }
};

document.querySelectorAll('[data-price-form]').forEach((form) => {
  const output = form.querySelector('[data-product-price]');
  if (!output) return;

  const updatePrice = () => {
    const baseCents = Number.parseInt(form.dataset.basePriceCents || '0', 10);
    const replacement = form.querySelector('[data-price-mode="replace"]:checked');
    let itemCents = replacement
      ? Number.parseInt(replacement.dataset.priceCents || '0', 10)
      : baseCents;

    let orderCents = 0;
    form.querySelectorAll('[data-price-mode="add"]:checked').forEach((input) => {
      const cents = Number.parseInt(input.dataset.priceCents || '0', 10);
      if (input.dataset.priceScope === 'order') orderCents += cents;
      else itemCents += cents;
    });

    const quantityInput = form.querySelector('input[name="quantity"]');
    const quantity = quantityInput
      ? Math.max(1, Number.parseInt(quantityInput.value || '1', 10) || 1)
      : 1;
    output.textContent = formatCommerceMoney((itemCents * quantity) + orderCents, form.dataset.currency);
  };

  form.addEventListener('change', updatePrice);
  form.addEventListener('input', (event) => {
    if (event.target.matches('input[name="quantity"]')) updatePrice();
  });
  updatePrice();
});

const mountEmbeddedCheckout = async () => {
  const mountPoint = document.querySelector('[data-stripe-checkout]');
  if (!mountPoint) return;

  const status = document.querySelector('#stripe-checkout-status');
  const setStatus = (message, isError = false) => {
    if (!status) return;
    status.textContent = message;
    status.classList.toggle('commerce-notice', isError);
    status.classList.toggle('commerce-notice-error', isError);
  };

  const publishableKey = mountPoint.dataset.publishableKey;
  const clientSecret = mountPoint.dataset.clientSecret;
  if (!publishableKey || !clientSecret) {
    setStatus('The secure payment form is unavailable. Please return to the cart and try again.', true);
    return;
  }
  if (typeof window.Stripe !== 'function') {
    setStatus('The secure payment service did not load. Check your connection and reload this page.', true);
    return;
  }

  try {
    const stripe = window.Stripe(publishableKey);
    const checkout = await stripe.initEmbeddedCheckout({
      fetchClientSecret: async () => clientSecret,
    });
    checkout.mount(mountPoint);
    setStatus('Secure payment form loaded.');
  } catch {
    setStatus('The secure payment form could not be loaded. Please return to the cart and try again.', true);
  }
};

mountEmbeddedCheckout();

const commerceError = document.querySelector('[data-commerce-error]');
commerceError?.focus();

document.querySelectorAll('[data-signage]').forEach((configurator) => {
  const image = configurator.querySelector('[data-signage-image]');
  const empty = configurator.querySelector('[data-signage-empty]');
  const status = configurator.querySelector('[data-signage-status]');
  const stand = configurator.querySelector('[data-signage-stand]');
  const updatePreview = () => {
    const size = configurator.querySelector('[data-signage-size]:checked');
    const withStand = stand.checked && !stand.disabled;
    const src = size ? (withStand ? size.dataset.standImage : size.dataset.image) : '';
    const description = size
      ? `${size.dataset.name} ${withStand ? 'with' : 'without'} wooden stand`
      : 'the selected configuration';
    image.hidden = true;
    image.removeAttribute('src');
    image.alt = `Saved photograph of ${description}`;
    empty.hidden = Boolean(src);
    empty.textContent = 'No saved photograph is available for this configuration yet.';
    status.textContent = src
      ? `Saved photograph: ${description}. Not a personalized proof.`
      : `No saved photograph for ${description}.`;
    if (src) {
      image.src = src;
      image.hidden = false;
    }
  };
  image.addEventListener('error', () => {
    image.hidden = true;
    empty.hidden = false;
    empty.textContent = 'The saved photograph could not be loaded. Please contact us for a preview.';
    status.textContent = 'Saved photograph unavailable.';
  });
  configurator.addEventListener('change', (event) => {
    if (event.target.matches('[data-signage-size], [data-signage-stand]')) updatePreview();
  });
  updatePreview();
});
