/**
 * Public-address validation for SSRF protection.
 *
 * Ported from the jina-ai/reader implementation (src/utils/ip.ts): IP/CIDR
 * parsing over Buffers plus a blocklist of non-public networks. On top of the
 * upstream list we also reject IPv4-mapped IPv6 addresses (::ffff:0:0/96,
 * tested against the IPv4 blocklist), 240.0.0.0/4 (reserved) and
 * 2001:db8::/32 (documentation), matching the strictness of the repo's Go
 * source-fetcher (ADR-0032).
 */

import { isIPv4, isIPv6 } from 'node:net';

export function parseIp(ip: string): Buffer {
  if (isIPv4(ip)) {
    const [a, b, c, d] = ip.split('.').map(Number) as [number, number, number, number];
    const buf = Buffer.alloc(4);
    buf.writeUInt8(a, 0);
    buf.writeUInt8(b, 1);
    buf.writeUInt8(c, 2);
    buf.writeUInt8(d, 3);
    return buf;
  }

  if (isIPv6(ip)) {
    if (ip.includes('.')) {
      // IPv4-in-IPv6 tail, e.g. `::ffff:127.0.0.1`. The dotted tail occupies
      // the final two 16-bit groups, so pad with two zero groups before
      // recursing. (jina-ai/reader pads with a single group, which shifts the
      // `::ffff` marker and corrupts the parsed address.)
      const parts = ip.split(':');
      const ipv4Part = parts.pop();
      if (!ipv4Part) throw new Error('Invalid IPv6 address');
      const ipv4Bytes = parseIp(ipv4Part);
      parts.push('0', '0');
      const ipv6Bytes = parseIp(parts.join(':'));
      ipv6Bytes.writeUInt32BE(ipv4Bytes.readUInt32BE(0), 12);
      return ipv6Bytes;
    }

    const buf = Buffer.alloc(16);

    let expanded = ip;
    if (ip.includes('::')) {
      const sides = ip.split('::');
      const left = sides[0] ? sides[0].split(':') : [];
      const right = sides[1] ? sides[1].split(':') : [];
      const middle = Array(8 - left.length - right.length).fill('0');
      expanded = [...left, ...middle, ...right].join(':');
    }

    const parts = expanded.split(':');
    let offset = 0;
    for (const part of parts) {
      buf.writeUInt16BE(parseInt(part, 16), offset);
      offset += 2;
    }
    return buf;
  }

  throw new Error('Invalid IP address');
}

function parseCIDR(cidr: string): [Buffer, Buffer] {
  const [ip, prefixTxt] = cidr.split('/') as [string, string];
  const buf = parseIp(ip);
  const maskBuf = Buffer.alloc(buf.byteLength, 0xff);
  const prefixBits = parseInt(prefixTxt);

  let offsetBits = 0;
  while (offsetBits < buf.byteLength * 8) {
    if (offsetBits <= prefixBits - 8) {
      offsetBits += 8;
      continue;
    }
    const bitsRemain = prefixBits - offsetBits;
    const byteOffset = Math.floor(offsetBits / 8);

    if (bitsRemain > 0) {
      const theByte = buf[byteOffset] ?? 0;
      const mask = 0xff << (8 - bitsRemain);
      maskBuf[byteOffset] = mask;
      buf[byteOffset] = theByte & mask;
      offsetBits += 8;
      continue;
    }
    buf[byteOffset] = 0;
    maskBuf[byteOffset] = 0;
    offsetBits += 8;
  }

  return [buf, maskBuf];
}

export class CIDR {
  private readonly buff: Buffer;
  private readonly mask: Buffer;

  constructor(cidr: string) {
    [this.buff, this.mask] = parseCIDR(cidr);
  }

  test(ip: string | Buffer): boolean {
    const parsedIp = typeof ip === 'string' ? parseIp(ip) : ip;

    if (parsedIp.byteLength !== this.buff.byteLength) {
      return false;
    }

    for (const i of Array(this.buff.byteLength).keys()) {
      const t = parsedIp[i] ?? 0;
      const m = this.mask[i] ?? 0;

      if (m === 0) {
        return true;
      }

      const r = this.buff[i];
      if ((t & m) !== r) {
        return false;
      }
    }

    return true;
  }
}

const nonPublicNetworks4 = [
  '10.0.0.0/8',
  '172.16.0.0/12',
  '192.168.0.0/16',

  '127.0.0.0/8',
  '255.255.255.255/32',
  '169.254.0.0/16',
  '224.0.0.0/4',

  '100.64.0.0/10',
  '0.0.0.0/8',
  '192.0.0.0/24',
  '192.0.2.0/24',
  '198.18.0.0/15',
  '198.51.100.0/24',
  '203.0.113.0/24',
  '240.0.0.0/4',
];

const nonPublicNetworks6 = [
  'fc00::/7',
  'fe80::/10',
  'ff00::/8',

  '::127.0.0.0/104',
  '::1/128',
  '::/128',
  '64:ff9b::/96',
  '2001:db8::/32',
];

const nonPublicCIDRs4 = nonPublicNetworks4.map((cidr) => new CIDR(cidr));
const nonPublicCIDRs6 = nonPublicNetworks6.map((cidr) => new CIDR(cidr));
const syntheticProxyCIDRs = [new CIDR('198.18.0.0/15'), new CIDR('fdfe:dcba:9876::/48')];

/** IPv4-mapped IPv6 range ::ffff:0:0/96, checked against the IPv4 blocklist. */
const ipv4MappedPrefix = parseIp('::ffff:0.0.0.0');

export function isIPInNonPublicRange(ip: string): boolean {
  const parsed = parseIp(ip);

  if (parsed.byteLength === 4) {
    return nonPublicCIDRs4.some((cidr) => cidr.test(parsed));
  }

  // IPv4-mapped IPv6 (::ffff:a.b.c.d): validate the embedded IPv4 address.
  const isMapped =
    parsed.subarray(0, 10).equals(ipv4MappedPrefix.subarray(0, 10)) &&
    parsed[10] === 0xff &&
    parsed[11] === 0xff;
  if (isMapped) {
    const embedded = parsed.subarray(12, 16);
    return nonPublicCIDRs4.some((cidr) => cidr.test(embedded));
  }

  return nonPublicCIDRs6.some((cidr) => cidr.test(parsed));
}

export function isPublicAddress(ip: string): boolean {
  return !isIPInNonPublicRange(ip);
}

/**
 * Addresses reserved by local transparent DNS proxies such as OrbStack or
 * Clash fake-IP mode. Callers may trust them only for a hostname lookup;
 * an IP-literal URL must still be rejected by the normal non-public guard.
 */
export function isSyntheticProxyAddress(ip: string): boolean {
  const parsed = parseIp(ip);
  return syntheticProxyCIDRs.some((cidr) => cidr.test(parsed));
}
