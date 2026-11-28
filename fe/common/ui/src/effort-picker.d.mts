export type EffortPickerConfig = {
  options: readonly {id:string;label:string}[];
  value: string;
  defaultValue?: string;
  onChange: (value:string)=>void;
  /** Set when the model offers no effort: the gauge stays, disabled, with this as its tooltip. */
  disabledReason?: string;
};
export function mountEffortPicker(host: HTMLElement, initial: EffortPickerConfig): {
  update(next: EffortPickerConfig): void;
  destroy(): void;
};
