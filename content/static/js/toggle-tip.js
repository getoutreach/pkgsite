/**
 * @license
 * Copyright 2019-2020 The Go Authors. All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

const toggles = document.querySelectorAll('[data-toggletip-content]');

/**
 * Adds event listeners to toggletip elements to display their
 * content when the toggle button is pressed. Used in the right
 * sidebar details section.
 */
toggles.forEach(toggle => {
  const message = toggle.getAttribute('data-toggletip-content');
  const tip = toggle.nextElementSibling;
  toggle.addEventListener('click', () => {
    tip.innerHTML = '';
    setTimeout(() => {
      tip.innerHTML = '<span class="UnitMetaDetails-toggletipBubble">' + message + '</span>';
    }, 100);
  });

  // Close on outside click
  document.addEventListener('click', e => {
    if (toggle !== e.target) {
      tip.innerHTML = '';
    }
  });

  // Remove toggletip on ESC
  toggle.addEventListener('keydown', e => {
    const ESC_KEY = 27;
    if ((e.keyCode || e.which) === ESC_KEY) {
      tip.innerHTML = '';
    }
  });
});
