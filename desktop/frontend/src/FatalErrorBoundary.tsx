import React from 'react';
import { claimAutomaticReload, recordFrontendDiagnostic } from './frontendDiagnostics';

type Props = React.PropsWithChildren<{ onReload?: () => void }>;
type State = { error: Error | null; reloading: boolean };

export class FatalErrorBoundary extends React.Component<Props, State> {
  state: State = { error: null, reloading: false };

  static getDerivedStateFromError(error: Error): State {
    return { error, reloading: false };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    recordFrontendDiagnostic('react.render', new Error(`${error.message}\n${info.componentStack}`));
    if (!claimAutomaticReload()) return;
    this.setState({ reloading: true });
    window.setTimeout(() => (this.props.onReload ?? (() => window.location.reload()))(), 100);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <main className="fatal-error" role="alert">
        <section className="fatal-error-card">
          <h1>{this.state.reloading ? 'Recovering AGX…' : 'AGX could not render this window'}</h1>
          <p>
            {this.state.reloading
              ? 'The interface hit an unexpected error and will reload once.'
              : 'Automatic recovery was already attempted. Your runtime and agent sessions are still running.'}
          </p>
          <pre>{this.state.error.message}</pre>
          {!this.state.reloading && <button onClick={() => (this.props.onReload ?? (() => window.location.reload()))()}>Reload interface</button>}
        </section>
      </main>
    );
  }
}

