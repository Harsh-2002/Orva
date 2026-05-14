/* =========================================================
   Orva Landing Page — app.js
   Vanilla JS — no dependencies.
   ========================================================= */

'use strict';

/* ── Copy to clipboard ─────────────────────────────────── */
function initCopyButtons() {
  document.querySelectorAll('.copy-btn').forEach(btn => {
    btn.addEventListener('click', async () => {
      const targetId = btn.dataset.target;
      const el = targetId ? document.getElementById(targetId) : null;
      if (!el) return;

      const text = el.textContent.trim();

      try {
        await navigator.clipboard.writeText(text);
      } catch {
        // Fallback for HTTP or older browsers
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.cssText = 'position:fixed;opacity:0;pointer-events:none';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
      }

      const original = btn.textContent;
      btn.textContent = 'Copied!';
      btn.classList.add('copied');
      setTimeout(() => {
        btn.textContent = original;
        btn.classList.remove('copied');
      }, 2000);
    });
  });
}

/* ── Tab switcher ──────────────────────────────────────── */
function initTabs() {
  const tabLists = document.querySelectorAll('[role="tablist"]');

  tabLists.forEach(list => {
    const btns   = list.querySelectorAll('[role="tab"]');
    const panels = list.closest('.install-wrap')
                       ?.querySelectorAll('.tab-panel') ?? [];

    btns.forEach(btn => {
      btn.addEventListener('click', () => {
        const target = btn.dataset.tab;

        btns.forEach(b => {
          b.classList.remove('active');
          b.setAttribute('aria-selected', 'false');
        });
        panels.forEach(p => {
          p.classList.remove('active');
        });

        btn.classList.add('active');
        btn.setAttribute('aria-selected', 'true');

        const panel = document.getElementById('tab-' + target);
        if (panel) panel.classList.add('active');
      });
    });

    // Keyboard navigation (←/→)
    list.addEventListener('keydown', e => {
      const current = list.querySelector('[aria-selected="true"]');
      const allBtns = Array.from(btns);
      const idx = allBtns.indexOf(current);

      if (e.key === 'ArrowRight') {
        allBtns[(idx + 1) % allBtns.length].click();
        allBtns[(idx + 1) % allBtns.length].focus();
        e.preventDefault();
      } else if (e.key === 'ArrowLeft') {
        allBtns[(idx - 1 + allBtns.length) % allBtns.length].click();
        allBtns[(idx - 1 + allBtns.length) % allBtns.length].focus();
        e.preventDefault();
      }
    });
  });
}

/* ── Lightbox ──────────────────────────────────────────── */
let lbLastFocus = null;

function openLightbox(src, caption) {
  const lb  = document.getElementById('lightbox');
  const img = document.getElementById('lb-img');
  const cap = document.getElementById('lb-caption');

  img.src = src;
  img.alt = caption;
  cap.textContent = caption;

  lb.classList.add('open');
  lb.setAttribute('aria-hidden', 'false');
  document.body.style.overflow = 'hidden';
  document.getElementById('lb-close').focus();
}

function closeLightbox() {
  const lb = document.getElementById('lightbox');
  lb.classList.remove('open');
  lb.setAttribute('aria-hidden', 'true');
  document.body.style.overflow = '';
  if (lbLastFocus) {
    lbLastFocus.focus();
    lbLastFocus = null;
  }
}

function initLightbox() {
  document.getElementById('lb-backdrop').addEventListener('click', closeLightbox);
  document.getElementById('lb-close').addEventListener('click', closeLightbox);

  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') closeLightbox();
  });

  document.querySelectorAll('[data-lightbox]').forEach(el => {
    el.addEventListener('click', () => {
      lbLastFocus = el;
      openLightbox(el.dataset.lightbox, el.dataset.caption ?? '');
    });

    el.addEventListener('keydown', e => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        lbLastFocus = el;
        openLightbox(el.dataset.lightbox, el.dataset.caption ?? '');
      }
    });
  });
}

/* ── Reveal on scroll ──────────────────────────────────── */
function initReveal() {
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    document.querySelectorAll('[data-reveal]').forEach(el => el.classList.add('visible'));
    return;
  }

  const observer = new IntersectionObserver(
    entries => {
      entries.forEach(entry => {
        if (entry.isIntersecting) {
          entry.target.classList.add('visible');
          observer.unobserve(entry.target);
        }
      });
    },
    { threshold: 0.08, rootMargin: '0px 0px -32px 0px' }
  );

  // Stagger siblings within grids
  ['features-grid', 'screenshots-grid', 'runtimes-grid'].forEach(cls => {
    document.querySelectorAll(`.${cls} [data-reveal]`).forEach((el, i) => {
      el.style.setProperty('--stagger', i % 3);
    });
  });

  document.querySelectorAll('[data-reveal]').forEach(el => observer.observe(el));
}

/* ── Back to top ───────────────────────────────────────── */
function initBackToTop() {
  const btn = document.getElementById('btt');
  if (!btn) return;

  window.addEventListener('scroll', () => {
    btn.classList.toggle('visible', window.scrollY > 420);
  }, { passive: true });

  btn.addEventListener('click', () => {
    window.scrollTo({ top: 0, behavior: 'smooth' });
  });
}

/* ── Smooth anchor scroll (32 px offset) ──────────────── */
function initAnchorScroll() {
  document.querySelectorAll('a[href^="#"]').forEach(link => {
    link.addEventListener('click', e => {
      const id = link.getAttribute('href').slice(1);
      const target = document.getElementById(id);
      if (!target) return;
      e.preventDefault();
      const top = target.getBoundingClientRect().top + window.scrollY - 32;
      window.scrollTo({ top, behavior: 'smooth' });
    });
  });
}

/* ── Boot ──────────────────────────────────────────────── */
document.addEventListener('DOMContentLoaded', () => {
  initCopyButtons();
  initTabs();
  initLightbox();
  initReveal();
  initBackToTop();
  initAnchorScroll();
});
