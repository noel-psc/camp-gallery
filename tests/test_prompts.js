const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {join} = require('node:path');
const {test} = require('node:test');
const vm = require('node:vm');
const source = readFileSync(join(__dirname, '../static/prompts.js'), 'utf8');

function setup({secure = false, modern = 'success', legacy = true} = {}) {
  const copied = [], selected = [], removed = [];
  const buttons = Array.from({length: 5}, (_, i) => ({
    dataset: {copyPrompt: String(i)}, disabled: false,
    addEventListener(type, handler) {this.click = handler;}, focus() {},
  }));
  const status = {textContent: ''};
  const manual = {hidden: true};
  const text = {value: '', focus() {}, select() {}, setSelectionRange() {}};
  const close = {addEventListener(type, handler) {this.click = handler;}};
  let input;
  const elements = {'prompt-tools': {querySelectorAll: () => buttons}, 'prompt-status': status,
    'prompt-manual': manual, 'prompt-manual-text': text, 'prompt-manual-close': close};
  const document = {
    getElementById: (id) => elements[id],
    body: {append(element) {input = element;}},
    createElement: () => ({style: {}, focus() {}, select() {},
      setSelectionRange(start, end) {selected.push([start, end]);}, remove() {removed.push(true);}}),
    execCommand(command) {
      assert.equal(command, 'copy');
      if (legacy === 'throw') throw new Error('unsupported');
      if (legacy) copied.push(input.value);
      return legacy;
    },
  };
  const navigator = secure ? {clipboard: {async writeText(value) {
    if (modern === 'reject') throw new Error('denied');
    copied.push(value);
  }}} : {};
  vm.runInNewContext(source, {document, navigator, window: {isSecureContext: secure}});
  return {buttons, status, manual, text, close, copied, selected, removed};
}

test('all five buttons copy full prompts over plain HTTP without exposing prompt bodies', async () => {
  const ui = setup();
  const starts = ['请参考我上传的人像照片', '请帮我润色我上传的故事', '请把我刚才润色好的故事', '请参考我上传的古风人物画像', '请把我上传的这张连环画插图'];
  for (let i = 0; i < 5; i++) {
    await ui.buttons[i].click();
    assert.ok(ui.copied[i].startsWith(starts[i]));
    assert.match(ui.copied[i], /要求：/);
    assert.equal(ui.manual.hidden, true);
    assert.equal(ui.buttons[i].disabled, false);
    assert.match(ui.status.textContent, /已复制/);
    assert.deepEqual(ui.selected[i], [0, ui.copied[i].length]);
  }
  assert.match(ui.copied[1], /　　这是故事的第一自然段。/);
  for (const style of ['画风A：黑白水墨画', '画风B：工笔重彩', '画风C：传统年画', '画风D：国风儿童插画']) assert.ok(ui.copied[3].includes(style));
  assert.equal(ui.removed.length, 5);
});

test('secure clipboard copies the full prompt and a permission denial falls back', async () => {
  for (const modern of ['success', 'reject']) {
    const ui = setup({secure: true, modern});
    await ui.buttons[0].click();
    assert.equal(ui.copied.length, 1);
    assert.match(ui.copied[0], /我穿越到古代啦/);
    assert.equal(ui.removed.length, modern === 'success' ? 0 : 1);
  }
});

test('failed copy reveals a selected manual fallback, never a false success', async () => {
  for (const legacy of [false, 'throw']) {
    const ui = setup({legacy});
    await ui.buttons[4].click();
    assert.equal(ui.manual.hidden, false);
    assert.match(ui.text.value, /让故事动起来啦/);
    assert.match(ui.status.textContent, /自动复制未成功/);
    assert.equal(ui.buttons[4].disabled, false);
    assert.equal(ui.removed.length, 1);
    ui.close.click();
    assert.equal(ui.manual.hidden, true);
    assert.equal(ui.text.value, '');
  }
});

test('loading on a page without prompt tools is harmless', () => {
  vm.runInNewContext(source, {document: {getElementById: () => null}});
});
