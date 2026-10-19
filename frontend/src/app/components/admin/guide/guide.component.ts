import { Component, Input, Optional } from '@angular/core';
import {
  NbAccordionModule,
  NbAlertModule,
  NbButtonModule,
  NbCardModule,
  NbDialogRef,
  NbIconModule,
  NbTooltipModule,
} from '@nebular/theme';

/**
 * The owner's manual. Pure content — it holds no state and calls no endpoint,
 * so it can be dropped into the manage page as a section and opened as a
 * dialog from the post-creation screen without any wiring.
 *
 * Everything documented here was read off the real components; when a screen
 * changes, this file changes with it.
 */
@Component({
  selector: 'app-guide',
  standalone: true,
  imports: [
    NbAccordionModule,
    NbCardModule,
    NbIconModule,
    NbButtonModule,
    NbAlertModule,
    NbTooltipModule,
  ],
  templateUrl: './guide.component.html',
  styleUrl: './guide.component.scss',
})
export class GuideComponent {
  /**
   * Set by the caller that opens the guide as its own dialog (the create
   * form). Not derived from the injected NbDialogRef: when the guide is a
   * section of the manage page there is no dialog to close, and a stray ref
   * from an enclosing dialog must not be closed by the guide's own button.
   */
  @Input() dialogMode = false;

  constructor(@Optional() private dialogRef: NbDialogRef<GuideComponent> | null) { }

  /** Example URL built from the real origin — the domain is never hardcoded. */
  readonly channelUrlExample = `${window.location.origin}/channel/my-channel`;

  close(): void {
    if (this.dialogMode) this.dialogRef?.close();
  }
}
