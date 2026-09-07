const products = [
  { name: 'Factory T-Shirt', price: 24 }, { name: 'Agent Notebook', price: 12 }, { name: 'Pipeline Mug', price: 16 }
];
const list = document.querySelector('#products');
document.querySelector('#search').addEventListener('input', (event) => {
  const query = event.target.value;
  list.innerHTML = products.filter((p) => p.name.includes(query)).map((p) => `<article><strong>${p.name}</strong><span>$${p.price}</span></article>`).join('');
});
document.querySelector('#search').dispatchEvent(new Event('input'));
