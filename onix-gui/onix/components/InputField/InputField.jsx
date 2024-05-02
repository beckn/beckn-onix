import React from "react";
import styles from "./InputField.module.css";

const InputField = ({ label, value, onChange }) => {
  return (
    <div className={styles.inputContainer}>
      <label className={styles.inputLabel}>{label}</label>
      <input
        placeholder="Input Text"
        className={styles.inputField}
        type="text"
        value={value}
        onChange={onChange}
      />
    </div>
  );
};

export default InputField;
