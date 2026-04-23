// alice admin UI — minimal progressive enhancement.
// Keep this file tiny: forms submit without JS, CSP blocks inline scripts,
// and no build step means every line you add ships verbatim to browsers.

(function () {
  'use strict';

  // Ask for confirmation before destructive actions. Reject buttons are
  // marked class="danger"; if the user cancels, we prevent form submission.
  document.addEventListener('submit', function (event) {
    var form = event.target;
    if (!form || form.nodeName !== 'FORM') {
      return;
    }
    var destructive = form.querySelector('button.danger');
    if (!destructive) {
      return;
    }
    if (!window.confirm('Are you sure? This is destructive.')) {
      event.preventDefault();
    }
  });
})();
