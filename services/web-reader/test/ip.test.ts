import { test } from 'node:test';
import assert from 'node:assert/strict';

import { isIPInNonPublicRange, isPublicAddress, isSyntheticProxyAddress } from '../src/ip.js';

test('public IPv4 addresses pass', () => {
  for (const ip of ['8.8.8.8', '1.1.1.1', '93.184.216.34', '172.15.255.255', '172.32.0.1', '193.168.1.1']) {
    assert.equal(isPublicAddress(ip), true, ip);
  }
});

test('non-public IPv4 addresses are blocked', () => {
  for (const ip of [
    '10.0.0.1',
    '172.16.0.5',
    '172.31.255.255',
    '192.168.1.1',
    '127.0.0.1',
    '0.0.0.0',
    '169.254.169.254',
    '100.64.0.1',
    '224.0.0.1',
    '240.0.0.1',
    '255.255.255.255',
  ]) {
    assert.equal(isIPInNonPublicRange(ip), true, ip);
  }
});

test('public IPv6 addresses pass', () => {
  for (const ip of ['2606:4700:4700::1111', '2001:4860:4860::8888']) {
    assert.equal(isPublicAddress(ip), true, ip);
  }
});

test('non-public IPv6 addresses are blocked', () => {
  for (const ip of ['::1', 'fe80::1', 'fc00::1', 'ff02::1', '2001:db8::1', '64:ff9b::192.0.2.33']) {
    assert.equal(isIPInNonPublicRange(ip), true, ip);
  }
});

test('IPv4-mapped IPv6 addresses are validated against the IPv4 blocklist', () => {
  assert.equal(isIPInNonPublicRange('::ffff:127.0.0.1'), true);
  assert.equal(isIPInNonPublicRange('::ffff:192.168.0.1'), true);
  assert.equal(isIPInNonPublicRange('::ffff:10.0.0.1'), true);
  assert.equal(isPublicAddress('::ffff:8.8.8.8'), true);
});

test('synthetic DNS proxy ranges are identified narrowly', () => {
  for (const ip of ['198.18.0.1', '198.19.255.254', 'fdfe:dcba:9876::26']) {
    assert.equal(isSyntheticProxyAddress(ip), true, ip);
    assert.equal(isIPInNonPublicRange(ip), true, ip);
  }
  for (const ip of ['127.0.0.1', '10.0.0.1', '198.20.0.1', 'fd00::1', '8.8.8.8']) {
    assert.equal(isSyntheticProxyAddress(ip), false, ip);
  }
});

test('invalid ip strings throw', () => {
  assert.throws(() => isIPInNonPublicRange('not-an-ip'));
});
