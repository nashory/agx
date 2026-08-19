import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react';
import { createPortal } from 'react-dom';
import { Check, ChevronDown } from 'lucide-react';

export type SelectOption = {
  value: string;
  label: string;
  disabled?: boolean;
};

type SelectProps = {
  value: string;
  options: SelectOption[];
  onChange: (value: string) => void;
  ariaLabel: string;
  disabled?: boolean;
  className?: string;
  menuMinWidth?: number;
};

type MenuPosition = {
  top: number;
  left: number;
  width: number;
};

const menuGap = 6;
const viewportPadding = 8;

export function Select({
  value,
  options,
  onChange,
  ariaLabel,
  disabled = false,
  className = '',
  menuMinWidth = 0,
}: SelectProps) {
  const listboxID = useId();
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const optionRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(-1);
  const [position, setPosition] = useState<MenuPosition | null>(null);
  const selectedIndex = options.findIndex((option) => option.value === value);
  const selectedOption = selectedIndex >= 0 ? options[selectedIndex] : undefined;

  const firstEnabledIndex = () => options.findIndex((option) => !option.disabled);

  const openMenu = (preferredIndex = selectedIndex) => {
    if (disabled || options.length === 0) return;
    const nextIndex = preferredIndex >= 0 && !options[preferredIndex]?.disabled
      ? preferredIndex
      : firstEnabledIndex();
    setActiveIndex(nextIndex);
    setOpen(true);
  };

  const closeMenu = (restoreFocus = false) => {
    setOpen(false);
    setPosition(null);
    if (restoreFocus) triggerRef.current?.focus();
  };

  const selectOption = (index: number) => {
    const option = options[index];
    if (!option || option.disabled) return;
    if (option.value !== value) onChange(option.value);
    closeMenu(true);
  };

  const moveActive = (direction: 1 | -1) => {
    if (options.length === 0) return;
    let index = activeIndex >= 0 ? activeIndex : selectedIndex;
    for (let count = 0; count < options.length; count += 1) {
      index = (index + direction + options.length) % options.length;
      if (!options[index].disabled) {
        setActiveIndex(index);
        return;
      }
    }
  };

  const updatePosition = () => {
    const trigger = triggerRef.current;
    if (!trigger) return;
    const rect = trigger.getBoundingClientRect();
    const width = Math.min(Math.max(rect.width, menuMinWidth), window.innerWidth - viewportPadding * 2);
    const measuredHeight = menuRef.current?.getBoundingClientRect().height
      ?? Math.min(options.length * 38 + 12, 280);
    const spaceBelow = window.innerHeight - rect.bottom - viewportPadding;
    const spaceAbove = rect.top - viewportPadding;
    const openAbove = measuredHeight > spaceBelow && spaceAbove > spaceBelow;
    const top = openAbove
      ? Math.max(viewportPadding, rect.top - measuredHeight - menuGap)
      : Math.min(window.innerHeight - measuredHeight - viewportPadding, rect.bottom + menuGap);
    const left = Math.min(
      Math.max(viewportPadding, rect.left),
      Math.max(viewportPadding, window.innerWidth - width - viewportPadding),
    );
    setPosition({ top: Math.max(viewportPadding, top), left, width });
  };

  useLayoutEffect(() => {
    if (!open) return;
    updatePosition();
    const activeOption = optionRefs.current[activeIndex];
    if (typeof activeOption?.scrollIntoView === 'function') {
      activeOption.scrollIntoView({ block: 'nearest' });
    }
  }, [open, activeIndex, options.length]);

  useEffect(() => {
    if (!open) return;

    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!rootRef.current?.contains(target) && !menuRef.current?.contains(target)) closeMenu();
    };
    const handleViewportChange = () => updatePosition();

    window.addEventListener('pointerdown', handlePointerDown);
    window.addEventListener('resize', handleViewportChange);
    window.addEventListener('scroll', handleViewportChange, true);
    return () => {
      window.removeEventListener('pointerdown', handlePointerDown);
      window.removeEventListener('resize', handleViewportChange);
      window.removeEventListener('scroll', handleViewportChange, true);
    };
  }, [open, options.length, menuMinWidth]);

  useEffect(() => {
    if (disabled && open) closeMenu();
  }, [disabled, open]);

  const handleKeyDown = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();
        if (!open) openMenu();
        else moveActive(1);
        break;
      case 'ArrowUp':
        event.preventDefault();
        if (!open) openMenu();
        else moveActive(-1);
        break;
      case 'Home':
        if (!open) return;
        event.preventDefault();
        setActiveIndex(firstEnabledIndex());
        break;
      case 'End':
        if (!open) return;
        event.preventDefault();
        for (let index = options.length - 1; index >= 0; index -= 1) {
          if (!options[index].disabled) {
            setActiveIndex(index);
            break;
          }
        }
        break;
      case 'Enter':
      case ' ':
        event.preventDefault();
        if (!open) openMenu();
        else selectOption(activeIndex);
        break;
      case 'Escape':
        if (!open) return;
        event.preventDefault();
        closeMenu();
        break;
      case 'Tab':
        if (open) closeMenu();
        break;
    }
  };

  return (
    <div ref={rootRef} className={`ui-select ${className}`.trim()}>
      <button
        ref={triggerRef}
        type="button"
        role="combobox"
        className={`ui-select-trigger ${open ? 'open' : ''}`}
        aria-label={ariaLabel}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listboxID : undefined}
        aria-activedescendant={open && activeIndex >= 0 ? `${listboxID}-option-${activeIndex}` : undefined}
        disabled={disabled}
        onClick={() => (open ? closeMenu() : openMenu())}
        onKeyDown={handleKeyDown}
      >
        <span>{selectedOption?.label ?? value}</span>
        <ChevronDown size={16} aria-hidden="true" />
      </button>
      {open && createPortal(
        <div
          ref={menuRef}
          id={listboxID}
          role="listbox"
          aria-label={ariaLabel}
          className={`ui-select-menu ${position ? 'positioned' : ''}`}
          style={position ?? undefined}
        >
          {options.map((option, index) => (
            <button
              ref={(element) => { optionRefs.current[index] = element; }}
              id={`${listboxID}-option-${index}`}
              key={option.value}
              type="button"
              role="option"
              aria-selected={option.value === value}
              className={`${option.value === value ? 'selected' : ''} ${index === activeIndex ? 'active' : ''}`.trim()}
              disabled={option.disabled}
              onPointerMove={() => !option.disabled && setActiveIndex(index)}
              onClick={() => selectOption(index)}
            >
              <span>{option.label}</span>
              {option.value === value && <Check size={15} aria-hidden="true" />}
            </button>
          ))}
        </div>,
        document.body,
      )}
    </div>
  );
}
