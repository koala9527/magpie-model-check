import path from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import { runInstallCheck } from './install-check.mjs';

export async function verifyClientProtocols(fixture) {
  const message = { role: 'user', content: 'ordinary synthetic business request' };
  const requests = [
    { protocol: 'OpenAI chat JSON', endpoint: '/v1/chat/completions', body: { messages: [message], max_tokens: 32 } },
    { protocol: 'OpenAI chat SSE', endpoint: '/v1/chat/completions', body: { messages: [message], max_tokens: 32, stream: true } },
    { protocol: 'Anthropic messages', endpoint: '/v1/messages', body: { messages: [message], max_tokens: 32 } },
    { protocol: 'OpenAI responses', endpoint: '/v1/responses', body: { input: message.content, max_output_tokens: 32 } },
  ];
  const results = [];
  for (const request of requests) {
    const response = await fetch(fixture.gateway + request.endpoint, {
      method: 'POST', headers: { Authorization: 'Bearer magpie', 'x-api-key': 'magpie', 'anthropic-version': '2023-06-01', 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: 'fixture/solo', ...request.body }), signal: AbortSignal.timeout(15000),
    });
    const text = await response.text();
    assert.equal(response.status, 200, `${request.protocol}: HTTP ${response.status}: ${text.slice(0, 300)}`);
    assert.ok(text.includes('ordinary fixture reply'), `${request.protocol}: ordinary reply was not preserved`);
    if (request.body.stream) assert.match(response.headers.get('content-type'), /text\/event-stream/);
    results.push({ protocol: request.protocol, response_status: response.status, ordinary_reply_preserved: true });
  }
  console.log('PASS: ordinary OpenAI chat JSON/SSE, Anthropic messages and OpenAI responses gateway wire APIs (HTTP clients, not external CLI validation)');
  return results;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const options = { afterReady: verifyClientProtocols }, args = process.argv.slice(2);
    for (let i = 0; i < args.length; i += 2) {
      const value = args[i + 1]; if (!value) throw Error(`Missing value for ${args[i]}`);
      if (args[i] === '--binary-dir') options.binaryDir = path.resolve(value);
      else if (args[i] === '--evidence-dir') options.evidenceDir = path.resolve(value);
      else if (args[i] === '--tarball') options.tarball = path.resolve(value);
      else throw Error(`Unknown client-check option: ${args[i]}`);
    }
    await runInstallCheck(options);
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
