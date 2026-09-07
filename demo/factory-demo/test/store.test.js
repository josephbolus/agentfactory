const test = require('node:test');
const assert = require('node:assert/strict');
const { filterProducts, calculateTotal } = require('../lib/store');

test('finds a product by its exact name', () => {
  assert.equal(filterProducts('Factory').length, 1);
});

test('adds a one-item cart', () => {
  assert.equal(calculateTotal([{ price: 24, quantity: 1 }]), 24);
});
