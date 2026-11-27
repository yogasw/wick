export type EffortPickerConfig = {
  options: readonly {id:string;label:string}[];
  value: string;
  defaultValue?: string;
  onChange: (value:string)=>void;
};
export function mountEffortPicker(host: HTMLElement, initial: EffortPickerConfig): {
  update(next: EffortPickerConfig): void;
  destroy(): void;
};
