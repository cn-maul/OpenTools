import type { ReactNode } from "react";

import { Button, Modal } from "@heroui/react";

type AppModalProps = {
  open: boolean;
  title: string;
  confirmLabel?: string;
  cancelLabel?: string;
  confirmDisabled?: boolean;
  onClose: () => void;
  onConfirm: () => void;
  children: ReactNode;
};

export default function AppModal({
  open,
  title,
  confirmLabel = "确认",
  cancelLabel = "取消",
  confirmDisabled = false,
  onClose,
  onConfirm,
  children,
}: AppModalProps) {
  return (
    <Modal isOpen={open} onOpenChange={(next) => !next && onClose()}>
      <Modal.Backdrop variant="blur" isDismissable>
        <Modal.Container placement="center" size="md" scroll="inside">
          <Modal.Dialog className="app-modal-dialog">
            <Modal.Header>
              <Modal.Heading className="app-modal-heading">{title}</Modal.Heading>
            </Modal.Header>
            <Modal.Body className="app-modal-body">{children}</Modal.Body>
            <Modal.Footer className="app-modal-footer">
              <Button variant="secondary" onPress={onClose}>
                {cancelLabel}
              </Button>
              <Button isDisabled={confirmDisabled} onPress={onConfirm}>
                {confirmLabel}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
