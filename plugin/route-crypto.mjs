// SHA-256/HMAC-SHA-256 using only ECMAScript primitives available in moejs.
// FIPS 180-4 / RFC 2104. Native crypto golden tests guard this small port.
const K = [0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,
  0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,
  0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,
  0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,
  0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,
  0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,
  0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,
  0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2];
function bytes(text) {
  const out = [];
  for (let i = 0; i < text.length; i++) {
    let cp = text.charCodeAt(i);
    if (cp >= 0xd800 && cp <= 0xdbff) {
      const next = text.charCodeAt(i + 1);
      if (next >= 0xdc00 && next <= 0xdfff) { cp = 0x10000 + ((cp - 0xd800) << 10) + next - 0xdc00; i++; }
      else cp = 0xfffd;
    } else if (cp >= 0xdc00 && cp <= 0xdfff) cp = 0xfffd;
    if (cp < 128) out.push(cp);
    else if (cp < 2048) out.push(192 | (cp >>> 6), 128 | (cp & 63));
    else if (cp < 65536) out.push(224 | (cp >>> 12), 128 | ((cp >>> 6) & 63), 128 | (cp & 63));
    else out.push(240 | (cp >>> 18), 128 | ((cp >>> 12) & 63), 128 | ((cp >>> 6) & 63), 128 | (cp & 63));
  }
  return out;
}
const rotate = (x, n) => (x >>> n) | (x << (32 - n));
function hash(input) {
  const data = input.slice(), bits = data.length * 8;
  data.push(128); while (data.length % 64 !== 56) data.push(0);
  const high = Math.floor(bits / 4294967296);
  for (let i = 3; i >= 0; i--) data.push((high >>> (i * 8)) & 255);
  for (let i = 3; i >= 0; i--) data.push((bits >>> (i * 8)) & 255);
  const h = [0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19];
  for (let pos = 0; pos < data.length; pos += 64) {
    const w = [];
    for (let j = 0; j < 16; j++) { const i = pos + j * 4; w[j] = (data[i] << 24) | (data[i+1] << 16) | (data[i+2] << 8) | data[i+3]; }
    for (let j = 16; j < 64; j++) {
      const a = w[j-15], b = w[j-2];
      w[j] = (w[j-16] + (rotate(a,7) ^ rotate(a,18) ^ (a >>> 3)) + w[j-7] + (rotate(b,17) ^ rotate(b,19) ^ (b >>> 10))) | 0;
    }
    let a=h[0], b=h[1], c=h[2], d=h[3], e=h[4], f=h[5], g=h[6], x=h[7];
    for (let j = 0; j < 64; j++) {
      const one = (x + (rotate(e,6) ^ rotate(e,11) ^ rotate(e,25)) + ((e & f) ^ (~e & g)) + K[j] + w[j]) | 0;
      const two = ((rotate(a,2) ^ rotate(a,13) ^ rotate(a,22)) + ((a & b) ^ (a & c) ^ (b & c))) | 0;
      x=g; g=f; f=e; e=(d+one)|0; d=c; c=b; b=a; a=(one+two)|0;
    }
    const v=[a,b,c,d,e,f,g,x]; for (let j=0;j<8;j++) h[j]=(h[j]+v[j])|0;
  }
  const out=[]; for (let j=0;j<8;j++) for (let i=3;i>=0;i--) out.push((h[j] >>> (i*8)) & 255);
  return out;
}
function hex(data) { let out=''; for (let i=0;i<data.length;i++) out += '0123456789abcdef'.charAt(data[i] >>> 4) + '0123456789abcdef'.charAt(data[i] & 15); return out; }
export function sha256(text) { return hex(hash(bytes(text))); }
export function hmac256(key, text) {
  let k=bytes(key); if (k.length>64) k=hash(k); while(k.length<64)k.push(0);
  const inside=[],outside=[]; for(let i=0;i<64;i++){inside.push(k[i]^0x36);outside.push(k[i]^0x5c);}
  return hex(hash(outside.concat(hash(inside.concat(bytes(text))))));
}

export function canonicalJSON(value){
  if(Array.isArray(value))return '['+value.map(canonicalJSON).join(',')+']';
  if(value!==null&&typeof value==='object')return '{'+Object.keys(value).sort().map(key=>JSON.stringify(key)+':'+canonicalJSON(value[key])).join(',')+'}';
  return JSON.stringify(value);
}
export function requestBodySHA256(body){
  if(!body||typeof body!=='object'||Array.isArray(body))return '';
  // Magpie chooses/rewrites model separately; its target and final model are
  // independently MAC-bound. The marker never belongs to the upstream packet.
  const keys=Object.keys(body).filter(key=>key!=='model'&&key!=='_magpie_model_check_probe').sort();
  return sha256('{'+keys.map(key=>JSON.stringify(key)+':'+canonicalJSON(body[key])).join(',')+'}');
}
