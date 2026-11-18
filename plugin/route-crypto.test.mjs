import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash, createHmac } from 'node:crypto';
import { sha256, hmac256 } from './route-crypto.mjs';

test('sandbox SHA-256 agrees with the native implementation, including UTF-8', () => {
  for (const text of ['', 'abc', '检测渠道 / 🐦', 'x'.repeat(1000)]) {
    assert.equal(sha256(text), createHash('sha256').update(text).digest('hex'));
  }
});
test('sandbox HMAC agrees with native HMAC and binds all fields', () => {
  for (const key of ['secret', 'k'.repeat(120), '本机密钥']) {
    for (const text of ['', 'route\nprobe\nprovider', '内容🦜']) {
      assert.equal(hmac256(key, text), createHmac('sha256', key).update(text).digest('hex'));
      assert.notEqual(hmac256(key, text), hmac256(key, text + '!'));
    }
  }
});
