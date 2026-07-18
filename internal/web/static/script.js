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

document.body.addEventListener('htmx:afterSwap', (event) => {
  const target = event.detail.target;
  if (target.id === 'contact-form' && window.turnstile) window.turnstile.render(target.querySelector('.cf-turnstile'));
});
