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

document.querySelector('[data-year]').textContent = new Date().getFullYear();

const contactForm = document.querySelector('[data-contact-form]');
const formStatus = document.querySelector('[data-form-status]');

contactForm?.addEventListener('submit', async (event) => {
  event.preventDefault();
  const button = contactForm.querySelector('button[type="submit"]');
  const fields = [...contactForm.querySelectorAll('input, textarea')];
  fields.forEach((field) => field.removeAttribute('aria-invalid'));

  if (!contactForm.checkValidity()) {
    contactForm.reportValidity();
    formStatus.textContent = 'Please complete the required fields.';
    formStatus.dataset.state = 'error';
    return;
  }

  button.disabled = true;
  formStatus.textContent = 'Sending…';
  formStatus.dataset.state = '';
  try {
    const payload = Object.fromEntries(new FormData(contactForm));
    const response = await fetch('/api/contact', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(payload),
    });
    const result = await response.json();
    if (!response.ok) {
      for (const name of Object.keys(result.errors || {})) contactForm.elements[name]?.setAttribute('aria-invalid', 'true');
      throw new Error(result.error || Object.values(result.errors || {})[0] || 'Could not send your message.');
    }
    contactForm.reset();
    formStatus.textContent = 'Message sent. We’ll be in touch.';
    formStatus.dataset.state = 'success';
  } catch (error) {
    formStatus.textContent = error.message || 'Could not send your message. Please email us instead.';
    formStatus.dataset.state = 'error';
  } finally {
    button.disabled = false;
  }
});
