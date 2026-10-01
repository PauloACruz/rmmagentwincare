import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { BackendContext, loadBackend } from './lib/backend';
import './styles.css';

const root = document.getElementById('root');
if (root) {
  void loadBackend().then((backend) => {
    createRoot(root).render(
      <StrictMode>
        <BackendContext.Provider value={backend}>
          <App />
        </BackendContext.Provider>
      </StrictMode>,
    );
  });
}
