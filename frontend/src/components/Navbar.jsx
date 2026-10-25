import { Link } from 'react-router-dom';

export default function Navbar() {
  return (
    <nav style={{ padding: '10px', background: '#eee', display: 'flex', gap: '15px' }}>
      <Link to="/">Home</Link>
      <Link to="/about">About</Link>
    </nav>
  );
}
