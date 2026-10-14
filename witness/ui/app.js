// Ark-0 Witness: the page asks this program for the node's status every few seconds and shows it.
// Whatever the node reports goes onto the page as text, never as HTML.
'use strict';

(() => {
  const STRINGS = window.STRINGS;
  const LANGS = ['en', 'zh', 'ja', 'ko', 'ru', 'es'];
  const NAMES = { en: 'English', zh: '中文', ja: '日本語', ko: '한국어', ru: 'Русский', es: 'Español' };
  const HTML_LANG = { en: 'en', zh: 'zh-CN', ja: 'ja', ko: 'ko', ru: 'ru', es: 'es' };
  const EXPLORER_HEIGHT = 'https://explorer.ark0.si/height/';
  // The two tripwires were created at height 15748: below it a node reports both as missing.
  const TRIPWIRES_CREATED = 15748;

  let lang = pickLanguage();
  let status = null; // the last status, or null when the program does not answer
  let check = null; // the last verdict on a pasted transaction
  let checkMessage = ''; // or why there is none

  function pickLanguage() {
    const tags = navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language || 'en'];
    for (const tag of tags) {
      const base = String(tag).toLowerCase().split('-')[0];
      if (LANGS.includes(base)) return base;
    }
    return 'en';
  }

  function t(key, values) {
    let text = (STRINGS[lang] && STRINGS[lang][key]) || STRINGS.en[key] || key;
    for (const [name, value] of Object.entries(values || {})) {
      text = text.split('{' + name + '}').join(String(value));
    }
    return text;
  }

  const $ = (id) => document.getElementById(id);
  const setText = (id, text) => { $(id).textContent = text; };
  const number = (n) => Number(n || 0).toLocaleString('en-US');

  function renderStatic() {
    document.documentElement.lang = HTML_LANG[lang];
    document.title = t('title');
    for (const el of document.querySelectorAll('[data-t]')) el.textContent = t(el.dataset.t);
  }

  function synced(s) {
    return s.node === 'running' && !s.ibd && s.headers > 0 && s.blocks >= s.headers && s.blocks >= TRIPWIRES_CREATED;
  }

  // The one-line verdict on top, and its tone.
  function verdict(s) {
    if (!s) return [t('verdict_unreachable'), 'bad'];
    if (s.node === 'stopped') return [t('verdict_stopped'), 'bad'];
    if (s.ark0 === 'mismatch') return [t('verdict_wrong_chain'), 'bad'];
    if (s.notM3) return [t('verdict_not_m3'), 'bad'];
    if (s.node !== 'running') return [t('verdict_starting'), 'warn'];
    if (!synced(s)) return [t('verdict_syncing'), 'warn'];
    if (s.ecDisabledNextBlock) return [t('verdict_pulled'), 'good'];
    if (s.blocks < s.h3) return [t('verdict_before_h3', { h3: number(s.h3) }), 'good'];
    return [t('verdict_armed'), 'good'];
  }

  function renderNode(s) {
    if (!s) {
      setText('sync', '');
      setText('peers', '');
      setText('ark0', '');
      setText('subversion', '');
      $('bar').style.width = '0';
      return;
    }
    if (s.node === 'stopped') {
      setText('sync', t('node_stopped', { msg: s.nodeError || '' }));
    } else if (s.node !== 'running') {
      setText('sync', s.warmup ? t('node_warmup', { msg: s.warmup }) : t('node_starting'));
    } else if (synced(s)) {
      setText('sync', t('node_synced', { blocks: number(s.blocks) }));
    } else {
      setText('sync', t('node_syncing', { blocks: number(s.blocks), headers: number(s.headers) }));
    }
    const ratio = s.headers > 0 ? Math.min(1, s.blocks / s.headers) : 0;
    $('bar').style.width = (ratio * 100).toFixed(1) + '%';
    setText('peers', s.node === 'running' ? (s.peers > 0 ? t('peers', { n: s.peers }) : t('peers_none')) : '');
    setText('ark0', s.node === 'running' ? t('ark0_' + s.ark0) : '');
    $('ark0').className = s.ark0 === 'ok' ? 'good' : s.ark0 === 'mismatch' ? 'bad' : '';
    setText('subversion', s.subversion ? t('subversion', { v: s.subversion }) : '');
  }

  function renderM3(s) {
    const h3 = s ? s.h3 : 30000;
    setText('m3-title', t('m3_title', { h3: number(h3) }));
    const reached = s && s.node === 'running' && s.blockH3;
    $('m3-hash').hidden = !reached;
    $('m3-link-line').hidden = !reached;
    if (!s || s.node !== 'running') {
      setText('m3', '');
    } else if (reached) {
      setText('m3', t('m3_after', { h3: number(h3) }));
      setText('m3-hash', s.blockH3);
      const link = $('m3-link');
      link.href = EXPLORER_HEIGHT + String(Number(h3));
      link.textContent = t('m3_compare');
    } else {
      setText('m3', t('m3_before', { n: number(Math.max(0, h3 - s.blocks)) }));
    }
  }

  function renderTripwires(s) {
    const body = $('tw');
    body.replaceChildren();
    const wires = s && s.node === 'running' ? s.tripwires || [] : [];
    if (wires.length === 0) {
      const row = body.insertRow();
      const cell = row.insertCell();
      cell.colSpan = 3;
      cell.textContent = t('tw_none');
      cell.className = 'dim';
    }
    for (const w of wires) {
      const row = body.insertRow();
      row.insertCell().textContent = w.label || '';
      const state = row.insertCell();
      state.textContent = w.state === 'unspent' ? t('state_unspent') : w.state === 'missing' ? t('state_missing') : w.state;
      const out = row.insertCell();
      out.textContent = w.outpoint || '';
      out.className = 'mono';
    }
    if (s && s.node === 'running' && !s.notM3) {
      setText('ec', s.ecDisabledNextBlock ? t('ec_off') : t('ec_on'));
      setText('ec-raw', '(ec_disabled_next_block: ' + String(Boolean(s.ecDisabledNextBlock)) + ')');
    } else {
      setText('ec', '');
      setText('ec-raw', '');
    }
  }

  function renderCheck() {
    const result = $('check-result');
    $('check-ec').hidden = !(check && check.ecDisabled);
    result.className = '';
    if (checkMessage) {
      result.textContent = checkMessage;
      result.className = 'bad';
    } else if (!check) {
      result.textContent = '';
    } else if (check.error) {
      result.textContent = t('check_node_error', { msg: check.error });
      result.className = 'bad';
    } else if (check.allowed) {
      result.textContent = t('check_allowed');
    } else {
      const reason = check.details && check.details !== check.reason ? check.reason + ' (' + check.details + ')' : check.reason;
      result.textContent = t('check_rejected', { reason: reason });
    }
  }

  function render() {
    const s = status;
    const [text, tone] = verdict(s);
    setText('verdict-text', text);
    $('verdict').className = 'card verdict ' + tone;
    renderNode(s);
    renderM3(s);
    renderTripwires(s);
    renderCheck();
    setText('data', s ? t('data_body', { path: s.dataDir }) : '');
    setText('updated', s ? t('updated', { time: s.now }) + ' · Ark0Witness ' + s.version : '');
  }

  async function refresh() {
    try {
      const response = await fetch('api/status', { cache: 'no-store' });
      status = response.ok ? await response.json() : null;
    } catch (e) {
      status = null;
    }
    render();
    await refreshReport();
  }

  async function refreshReport() {
    const area = $('report');
    if (document.activeElement === area) return; // leave a selection alone
    try {
      const response = await fetch('api/report', { cache: 'no-store' });
      if (response.ok) area.value = await response.text();
    } catch (e) {
      // keep the last report
    }
  }

  async function runCheck() {
    const button = $('check');
    const hex = $('hex').value;
    check = null;
    checkMessage = '';
    if (!hex.trim()) {
      checkMessage = t('check_empty');
      renderCheck();
      return;
    }
    button.disabled = true;
    setText('check-result', t('check_working'));
    try {
      const response = await fetch('api/check', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ hex: hex }),
      });
      const answer = await response.json();
      if (response.ok) {
        check = answer;
      } else {
        checkMessage = answer.error === 'hex' ? t('check_hex_error') : t('check_not_ready');
      }
    } catch (e) {
      checkMessage = t('check_not_ready');
    }
    button.disabled = false;
    renderCheck();
    await refreshReport();
  }

  async function copyReport() {
    const area = $('report');
    try {
      await navigator.clipboard.writeText(area.value);
      setText('copied', t('copied'));
    } catch (e) {
      area.focus();
      area.select();
      setText('copied', document.execCommand('copy') ? t('copied') : t('copy_failed'));
    }
  }

  function setup() {
    const select = $('lang');
    for (const code of LANGS) {
      const option = document.createElement('option');
      option.value = code;
      option.textContent = NAMES[code];
      select.appendChild(option);
    }
    select.value = lang;
    select.addEventListener('change', () => {
      lang = LANGS.includes(select.value) ? select.value : 'en';
      renderStatic();
      render();
    });
    $('check').addEventListener('click', runCheck);
    $('copy').addEventListener('click', copyReport);
    renderStatic();
    render();
    refresh();
    setInterval(refresh, 5000);
  }

  setup();
})();
