const products = [
  { id: 1, name: 'Factory T-Shirt', price: 24 },
  { id: 2, name: 'Agent Notebook', price: 12 },
  { id: 3, name: 'Pipeline Mug', price: 16 }
];

function filterProducts(query) {
  const normalizedQuery = query.trim();
  // Intentional demo bug: this search is incorrectly case-sensitive.
  return products.filter((product) => product.name.includes(normalizedQuery));
}

function calculateTotal(items) {
  // Intentional demo bug: quantity is ignored.
  return items.reduce((total, item) => total + item.price, 0);
}

module.exports = { products, filterProducts, calculateTotal };
