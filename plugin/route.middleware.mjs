// Official moejs hooks: this file performs no I/O and never changes routing.
// The Bun entry provisions an unexported, per-service key outside page/.
import { routeKey } from './.runtime/route-secret.mjs';
import { sha256, hmac256, requestBodySHA256, canonicalJSON } from './route-crypto.mjs';

const markerKey = '_magpie_model_check_probe', evidenceKey = '_magpie_model_check_route';
export function onRequest(body, ctx) {
  const marker = body && body[markerKey];
  if (!marker || typeof marker !== 'object' || Array.isArray(marker) || Object.keys(marker).length !== 2
      || marker.version !== 1 || typeof marker.id !== 'string' || !/^[a-f0-9]{32}$/.test(marker.id)) return body;
  ctx.state.modelCheckProbe = { id: marker.id, target: body.model, attempts: 0, provider: null };
  delete body[markerKey];
  return body;
}
export function onProvider(body, ctx) {
  const probe = ctx.state.modelCheckProbe;
  if (!probe) return body;
  // Clear previous-attempt authority before any bounded/hash work can fail.
  probe.provider = null;
  probe.requestSHA256 = '';
  probe.attempts++;
  probe.requestSHA256 = requestBodySHA256(body);
  const p = ctx.provider;
  probe.provider = p && typeof p.id === 'string' && typeof p.model === 'string' ? { id: p.id, model: p.model } : null;
  return body;
}
function observe(body, ctx, streaming=false) {
  const probe = ctx.state.modelCheckProbe;
  if (!probe || !body || typeof body !== 'object' || Array.isArray(body)) return body;
  // Remove upstream claims even if this instance has no signing key.
  delete body[evidenceKey];
  const current=ctx.provider;
  if (typeof routeKey !== 'string' || routeKey.length !== 64 || !probe.provider || !current
      || current.id!==probe.provider.id || current.model!==probe.provider.model) return body;
  const choice = body.choices && body.choices[0];
  const content = choice && (choice.message || choice.delta);
  const text = content && typeof content.content === 'string' ? content.content : '';
  const finish = choice && typeof choice.finish_reason === 'string' ? choice.finish_reason : '';
  const digest = sha256(text + '\n' + finish);
  const envelopeDigest=sha256(canonicalJSON(body));
  const streamDigest=streaming?sha256((probe.streamSHA256||'')+'\n'+envelopeDigest):'';
  if(streaming)probe.streamSHA256=streamDigest;
  const requestDigest=probe.requestSHA256||'';
  const payload = ['model-check-route-v3', probe.id, probe.target, probe.provider.id, probe.provider.model, String(probe.attempts), digest, requestDigest,envelopeDigest,streamDigest].join('\n');
  body[evidenceKey] = { version: 3, probe_id: probe.id, target_model_id: probe.target,
    provider_id: probe.provider.id, provider_model: probe.provider.model, attempt_count: probe.attempts,
    source: 'local-gateway-middleware', response_sha256: digest, request_sha256:requestDigest,
    envelope_sha256:envelopeDigest,stream_sha256:streamDigest, mac: hmac256(routeKey, payload) };
  return body;
}
export function onResponse(body, ctx) { return observe(body, ctx); }
export function onEvent(body, ctx) { return observe(body, ctx,true); }
