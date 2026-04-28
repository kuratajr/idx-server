const nav = document.querySelector('.nav');
const toggle = document.querySelector('.nav-toggle');
const copyButtons = document.querySelectorAll('[data-target]');
const header = document.querySelector('.site-header');
const langToggle = document.getElementById('langToggle');

if (toggle && nav) {
    toggle.addEventListener('click', () => {
        const isOpen = nav.getAttribute('data-open') === 'true';
        nav.setAttribute('data-open', String(!isOpen));
        toggle.setAttribute('aria-expanded', String(!isOpen));
    });

    document.addEventListener('click', (event) => {
        if (!nav.contains(event.target) && !toggle.contains(event.target)) {
            nav.setAttribute('data-open', 'false');
            toggle.setAttribute('aria-expanded', 'false');
        }
    });
}

/** Clipboard API often fails on non-HTTPS (except localhost) or when permission denied. */
function copyTextFallback(text) {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.left = '-9999px';
    ta.style.top = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    ta.setSelectionRange(0, text.length);
    let ok = false;
    try {
        ok = document.execCommand('copy');
    } catch {
        ok = false;
    } finally {
        document.body.removeChild(ta);
    }
    return ok;
}

async function copyTextToClipboard(text) {
    if (navigator.clipboard && window.isSecureContext) {
        try {
            await navigator.clipboard.writeText(text);
            return true;
        } catch {
            /* fall through */
        }
    }
    return copyTextFallback(text);
}

if (copyButtons.length) {
    copyButtons.forEach((button) => {
        button.addEventListener('click', async () => {
            const targetId = button.getAttribute('data-target');
            const code = document.getElementById(targetId);
            if (!code) return;

            const text = code.textContent.trim();
            const originalText = button.textContent;
            const i18n = window.XTProI18n;

            const ok = await copyTextToClipboard(text);
            if (ok) {
                button.textContent = i18n ? `✅ ${i18n.t('common.copy.copied')}` : '✅ Copied!';
            } else {
                button.textContent = i18n ? i18n.t('common.copy.failed') : 'Copy failed';
            }
            setTimeout(() => {
                const i18nLater = window.XTProI18n;
                const key = button.getAttribute('data-i18n');
                if (i18nLater && key) {
                    button.textContent = i18nLater.t(key);
                } else {
                    button.textContent = originalText;
                }
            }, 1800);
        });
    });
}

if (langToggle) {
    const updateLabel = () => {
        const i18n = window.XTProI18n;
        const current = i18n?.getLang?.() || 'vi';
        langToggle.textContent = current === 'vi' ? 'EN' : 'VI';
    };

    langToggle.addEventListener('click', async () => {
        const i18n = window.XTProI18n;
        if (!i18n) return;
        const next = i18n.getLang() === 'vi' ? 'en' : 'vi';
        i18n.setLang(next);
        await i18n.loadDict(next);
        i18n.applyTranslations(document);
        updateLabel();
    });

    window.addEventListener('xtpro:i18n:ready', updateLabel);
    window.addEventListener('xtpro:i18n:applied', updateLabel);
    updateLabel();
}

if (header) {
    const observer = new IntersectionObserver(
        ([entry]) => {
            if (!entry.isIntersecting) {
                header.classList.add('is-solid');
            } else {
                header.classList.remove('is-solid');
            }
        },
        { rootMargin: '-120px 0px 0px 0px' }
    );

    observer.observe(document.body);
}
