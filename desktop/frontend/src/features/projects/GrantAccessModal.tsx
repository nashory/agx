import type { Project } from '../../types';

export function GrantAccessModal({
  project,
  granting,
  error,
  onBack,
  onGrant,
}: {
  project: Project;
  granting: boolean;
  error?: string;
  onBack: () => void;
  onGrant: () => void;
}) {
  return (
    <div className="modal-backdrop blurred access-backdrop">
      <section className="project-access-modal" role="dialog" aria-modal="true" aria-labelledby="grant-access-title">
        <header className="modal-header">
          <div>
            <h2 id="grant-access-title">Grant Access</h2>
            <p>{project.name}</p>
          </div>
        </header>
        <div className="access-modal-body">
          <p>AGX needs to verify that it can create and write task worktrees for this project before agents run.</p>
          <code>{project.path}</code>
          {(error || project.accessError) && (
            <div className="access-modal-error" role={error ? 'alert' : undefined}>
              {error || project.accessError}
            </div>
          )}
        </div>
        <footer className="wizard-actions">
          <button className="text-button" disabled={granting} onClick={onBack}>
            Back
          </button>
          <button className="primary-button" disabled={granting} onClick={onGrant}>
            {granting ? 'Granting...' : 'Grant Access'}
          </button>
        </footer>
      </section>
    </div>
  );
}
