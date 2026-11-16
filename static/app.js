(() => {
  'use strict';
  document.querySelectorAll('[data-print]').forEach((button) => {
    button.addEventListener('click', () => window.print());
  });
  document.querySelectorAll('[data-back]').forEach((button) => {
    button.addEventListener('click', () => history.length > 1 ? history.back() : location.assign('/'));
  });
  document.addEventListener('click', (event) => {
    const button = event.target.closest('[data-confirm]');
    if (button && !window.confirm(button.dataset.confirm)) event.preventDefault();
  });
})();
